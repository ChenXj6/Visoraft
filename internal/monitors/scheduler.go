package monitors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/tasks"
)

type Scheduler struct {
	store      *PostgresStore
	discoverer *Discoverer
	tasks      *tasks.Service
	logger     *slog.Logger
	owner      string
	poll       time.Duration
	now        func() time.Time
}

func NewScheduler(
	store *PostgresStore,
	discoverer *Discoverer,
	taskService *tasks.Service,
	logger *slog.Logger,
	owner string,
	poll time.Duration,
) *Scheduler {
	if poll <= 0 {
		poll = 3 * time.Second
	}
	return &Scheduler{
		store:      store,
		discoverer: discoverer,
		tasks:      taskService,
		logger:     logger,
		owner:      owner,
		poll:       poll,
		now:        time.Now,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		if err := s.runCycle(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("youtube scheduler cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context) error {
	now := s.now().UTC()
	requeued, err := s.store.RequeueExpiredRuns(ctx, now)
	if err != nil {
		return err
	}
	if requeued > 0 {
		s.logger.Warn("requeued expired youtube monitor runs", "count", requeued)
	}
	enqueued, err := s.store.EnqueueDue(ctx, now)
	if err != nil {
		return err
	}
	if enqueued > 0 {
		s.logger.Info("enqueued scheduled youtube monitors", "count", enqueued)
	}

	for processed := 0; processed < 10; processed++ {
		run, err := s.store.ClaimRun(ctx, s.owner)
		if errors.Is(err, ErrRunNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		s.execute(ctx, run)
	}
	return nil
}

func (s *Scheduler) execute(ctx context.Context, run ClaimedRun) {
	candidates, quotaUnits, discoveryErr := s.discoverer.Discover(ctx, run.Monitor)
	if discoveryErr != nil {
		if err := s.store.CompleteRun(
			ctx,
			run,
			quotaUnits,
			discoveryErr,
			s.now().UTC(),
		); err != nil {
			s.logger.Error(
				"could not persist failed youtube monitor run",
				"run_id", run.Run.ID,
				"error", err,
			)
		}
		return
	}

	for _, candidate := range candidates {
		if err := s.processCandidate(ctx, run, candidate); err != nil {
			s.logger.Error(
				"youtube monitor candidate failed",
				"run_id", run.Run.ID,
				"external_video_id", candidate.ExternalVideoID,
				"error", err,
			)
			_ = s.store.RecordItem(
				ctx,
				run.Run,
				run.Monitor,
				candidate,
				"task_failed",
				err.Error(),
				nil,
				s.now().UTC(),
			)
		}
	}
	if err := s.store.CompleteRun(
		ctx,
		run,
		quotaUnits,
		nil,
		s.now().UTC(),
	); err != nil {
		s.logger.Error(
			"could not complete youtube monitor run",
			"run_id", run.Run.ID,
			"error", err,
		)
	}
}

func (s *Scheduler) processCandidate(
	ctx context.Context,
	run ClaimedRun,
	candidate Candidate,
) error {
	if candidate.ExternalVideoID == "" || candidate.SourceURL == "" {
		return fmt.Errorf("discovery returned an incomplete candidate")
	}
	now := s.now().UTC()
	if passed, reason := candidatePasses(run.Monitor, candidate); !passed {
		return s.store.RecordItem(
			ctx,
			run.Run,
			run.Monitor,
			candidate,
			"filtered",
			reason,
			nil,
			now,
		)
	}
	seen, err := s.store.Seen(
		ctx,
		run.Monitor.ID,
		candidate.ExternalVideoID,
	)
	if err != nil {
		return err
	}
	if seen {
		return s.store.RecordItem(
			ctx,
			run.Run,
			run.Monitor,
			candidate,
			"duplicate",
			"该监控此前已发现此视频，可核对后加入任务",
			nil,
			now,
		)
	}
	if !run.Monitor.AutoAddToTasks {
		if err := s.store.MarkSeen(
			ctx,
			run.Monitor.ID,
			candidate.ExternalVideoID,
			run.Run.ID,
			now,
		); err != nil {
			return err
		}
		return s.store.RecordItem(
			ctx,
			run.Run,
			run.Monitor,
			candidate,
			"accepted",
			"符合筛选条件，配置为仅记录不建单",
			nil,
			now,
		)
	}

	reserved, err := s.store.ReserveIngestion(
		ctx,
		candidate.ExternalVideoID,
		run.Monitor.ID,
		now,
	)
	if err != nil {
		return err
	}
	if !reserved {
		_ = s.store.MarkSeen(
			ctx,
			run.Monitor.ID,
			candidate.ExternalVideoID,
			run.Run.ID,
			now,
		)
		return s.store.RecordItem(
			ctx,
			run.Run,
			run.Monitor,
			candidate,
			"duplicate",
			"该视频已由其他监控创建任务",
			nil,
			now,
		)
	}

	task, err := s.tasks.Create(ctx, tasks.CreateInput{
		SourceURL:              candidate.SourceURL,
		TargetPlatforms:        run.Monitor.TaskTemplate.TargetPlatforms,
		CookieProfileID:        run.Monitor.TaskTemplate.CookieProfileID,
		RepostStatementVersion: run.Monitor.TaskTemplate.RepostStatementVersion,
		PostingStrategyID:      run.Monitor.TaskTemplate.PostingStrategyID,
		AutoPublish:            run.Monitor.TaskTemplate.AutoPublish,
		Origin: &tasks.TaskOrigin{
			Kind:            "monitor",
			MonitorID:       run.Monitor.ID,
			MonitorName:     run.Monitor.Name,
			SeriesTitle:     run.Monitor.SeriesTitle,
			SeriesScopeKey:  candidate.SeriesScopeKey,
			SeriesScopeName: candidate.SeriesScopeName,
			EpisodeNumber:   candidate.EpisodeNumber,
		},
	})
	if err != nil {
		_ = s.store.ReleaseIngestion(ctx, candidate.ExternalVideoID)
		return fmt.Errorf("create task from monitor: %w", err)
	}
	if err := s.store.FinalizeIngestion(
		ctx,
		candidate.ExternalVideoID,
		task.ID,
		run.Monitor.ID,
		run.Run.ID,
		now,
	); err != nil {
		return fmt.Errorf("finalize task ingestion: %w", err)
	}
	return s.store.RecordItem(
		ctx,
		run.Run,
		run.Monitor,
		candidate,
		"task_created",
		"符合筛选条件，已进入统一任务流水线",
		&task.ID,
		now,
	)
}

func candidatePasses(monitor Monitor, candidate Candidate) (bool, string) {
	if !slices.Contains(monitor.VideoTypes, candidate.VideoType) {
		return false, "视频类型不在允许范围"
	}
	if slices.Contains(monitor.ExcludeChannelIDs, candidate.ChannelID) {
		return false, "频道位于排除列表"
	}
	searchable := strings.ToLower(candidate.Title + "\n" + candidate.Description)
	if len(monitor.IncludeKeywords) > 0 {
		matched := false
		for _, keyword := range monitor.IncludeKeywords {
			if strings.Contains(searchable, strings.ToLower(keyword)) {
				matched = true
				break
			}
		}
		if !matched {
			return false, "未命中包含关键词"
		}
	}
	for _, keyword := range monitor.ExcludeKeywords {
		if strings.Contains(searchable, strings.ToLower(keyword)) {
			return false, "命中排除关键词：" + keyword
		}
	}
	if candidate.ViewCount < monitor.MinViewCount {
		return false, "观看数低于阈值"
	}
	if candidate.LikeCount < monitor.MinLikeCount {
		return false, "点赞数低于阈值"
	}
	if candidate.CommentCount < monitor.MinCommentCount {
		return false, "评论数低于阈值"
	}
	if candidate.DurationSeconds < monitor.MinDurationSeconds {
		return false, "时长低于阈值"
	}
	if monitor.MaxDurationSeconds > 0 &&
		candidate.DurationSeconds > monitor.MaxDurationSeconds {
		return false, "时长高于阈值"
	}
	return true, ""
}
