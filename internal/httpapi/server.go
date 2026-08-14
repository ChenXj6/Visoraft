package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/visoraft/visoraft/internal/cookieprofiles"
	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/monitors"
	"github.com/visoraft/visoraft/internal/objectstorage"
	"github.com/visoraft/visoraft/internal/publishing"
	"github.com/visoraft/visoraft/internal/reviews"
	appsettings "github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/tasks"
)

type Server struct {
	service         *tasks.Service
	cookieService   *cookieprofiles.Service
	settingsService *appsettings.Service
	reviewService   *reviews.Service
	monitorService  *monitors.Service
	publishService  *publishing.Service
	objectStorage   *objectstorage.Client
	pool            *pgxpool.Pool
	logger          *slog.Logger
	startedAt       time.Time
	version         string
	workerToken     string
	handler         http.Handler
}

type errorBody struct {
	Error problem `json:"error"`
}

type problem struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func NewServer(
	service *tasks.Service,
	cookieService *cookieprofiles.Service,
	settingsService *appsettings.Service,
	reviewService *reviews.Service,
	monitorService *monitors.Service,
	publishService *publishing.Service,
	objectStorage *objectstorage.Client,
	pool *pgxpool.Pool,
	logger *slog.Logger,
	version string,
	workerToken string,
) *Server {
	server := &Server{
		service:         service,
		cookieService:   cookieService,
		settingsService: settingsService,
		reviewService:   reviewService,
		monitorService:  monitorService,
		publishService:  publishService,
		objectStorage:   objectStorage,
		pool:            pool,
		logger:          logger,
		startedAt:       time.Now().UTC(),
		version:         version,
		workerToken:     workerToken,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", server.live)
	mux.HandleFunc("GET /health/ready", server.ready)
	mux.HandleFunc("GET /api/v1/system/status", server.systemStatus)
	mux.HandleFunc("GET /api/v1/dashboard", server.dashboard)
	mux.HandleFunc("GET /api/v1/files", server.fileLibrary)
	mux.HandleFunc("GET /api/v1/tasks", server.listTasks)
	mux.HandleFunc("POST /api/v1/tasks", server.createTask)
	mux.HandleFunc("POST /api/v1/tasks/bulk-retry", server.bulkRetryTasks)
	mux.HandleFunc("GET /api/v1/tasks/archive-preview", server.taskArchivePreview)
	mux.HandleFunc("POST /api/v1/tasks/archive-all", server.archiveAllTasks)
	mux.HandleFunc("GET /api/v1/tasks/{taskID}", server.getTask)
	mux.HandleFunc(
		"GET /api/v1/tasks/{taskID}/assets/{assetID}/content",
		server.getAssetContent,
	)
	mux.HandleFunc("POST /api/v1/tasks/{taskID}/cancel", server.cancelTask)
	mux.HandleFunc("POST /api/v1/tasks/{taskID}/retry", server.retryTask)
	mux.HandleFunc("POST /api/v1/tasks/{taskID}/archive", server.archiveTask)
	mux.HandleFunc("POST /api/v1/tasks/{taskID}/restore", server.restoreTask)
	mux.HandleFunc("DELETE /api/v1/tasks/{taskID}", server.purgeTask)
	mux.HandleFunc("PUT /api/v1/tasks/{taskID}/cookie-profile", server.setTaskCookieProfile)
	mux.HandleFunc("DELETE /api/v1/tasks/{taskID}/assets", server.deleteTaskAssets)
	mux.HandleFunc("GET /api/v1/cookie-profiles", server.listCookieProfiles)
	mux.HandleFunc("POST /api/v1/cookie-profiles/upload", server.uploadCookieProfile)
	mux.HandleFunc("POST /api/v1/cookie-profiles/cookiecloud", server.createCookieCloudProfile)
	mux.HandleFunc("POST /api/v1/cookie-profiles/{profileID}/sync", server.syncCookieProfile)
	mux.HandleFunc("DELETE /api/v1/cookie-profiles/{profileID}", server.deleteCookieProfile)
	mux.HandleFunc("GET /api/v1/settings", server.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", server.updateSettings)
	mux.HandleFunc("POST /api/v1/settings/test-connection", server.testSettingsConnection)
	mux.HandleFunc("GET /api/v1/reviews", server.listReviews)
	mux.HandleFunc("GET /api/v1/reviews/{taskID}", server.getReview)
	mux.HandleFunc("PUT /api/v1/reviews/{taskID}/metadata", server.updateReviewMetadata)
	mux.HandleFunc(
		"PUT /api/v1/reviews/{taskID}/subtitles/{documentID}",
		server.updateReviewSubtitle,
	)
	mux.HandleFunc("POST /api/v1/reviews/{taskID}/{action}", server.actOnReview)
	mux.HandleFunc("GET /api/v1/youtube-monitors", server.listYouTubeMonitors)
	mux.HandleFunc("POST /api/v1/youtube-monitors", server.createYouTubeMonitor)
	mux.HandleFunc("GET /api/v1/youtube-categories", server.listYouTubeCategories)
	mux.HandleFunc("GET /api/v1/youtube-monitors/{monitorID}", server.getYouTubeMonitor)
	mux.HandleFunc("PUT /api/v1/youtube-monitors/{monitorID}", server.updateYouTubeMonitor)
	mux.HandleFunc("DELETE /api/v1/youtube-monitors/{monitorID}", server.deleteYouTubeMonitor)
	mux.HandleFunc("POST /api/v1/youtube-monitors/{monitorID}/pause", server.pauseYouTubeMonitor)
	mux.HandleFunc("POST /api/v1/youtube-monitors/{monitorID}/resume", server.resumeYouTubeMonitor)
	mux.HandleFunc("POST /api/v1/youtube-monitors/{monitorID}/run", server.runYouTubeMonitor)
	mux.HandleFunc("GET /api/v1/youtube-monitors/{monitorID}/history", server.youtubeMonitorHistory)
	mux.HandleFunc("POST /api/v1/youtube-monitors/{monitorID}/tasks", server.enqueueYouTubeMonitorItems)
	mux.HandleFunc("GET /api/v1/platform-accounts", server.listPlatformAccounts)
	mux.HandleFunc("POST /api/v1/platform-accounts", server.createPlatformAccount)
	mux.HandleFunc("GET /api/v1/platform-accounts/{accountID}", server.getPlatformAccount)
	mux.HandleFunc("PUT /api/v1/platform-accounts/{accountID}", server.updatePlatformAccount)
	mux.HandleFunc("DELETE /api/v1/platform-accounts/{accountID}", server.archivePlatformAccount)
	mux.HandleFunc(
		"POST /api/v1/platform-accounts/{accountID}/check",
		server.checkPlatformAccount,
	)
	mux.HandleFunc("GET /api/v1/platform-categories", server.listPlatformCategories)
	mux.HandleFunc("POST /api/v1/platform-categories/refresh", server.refreshPlatformCategories)
	mux.HandleFunc("GET /api/v1/transcode-presets", server.listTranscodePresets)
	mux.HandleFunc("POST /api/v1/transcode-presets", server.createTranscodePreset)
	mux.HandleFunc("GET /api/v1/transcode-presets/{presetID}", server.getTranscodePreset)
	mux.HandleFunc("PUT /api/v1/transcode-presets/{presetID}", server.updateTranscodePreset)
	mux.HandleFunc("DELETE /api/v1/transcode-presets/{presetID}", server.archiveTranscodePreset)
	mux.HandleFunc("GET /api/v1/posting-strategies", server.listPostingStrategies)
	mux.HandleFunc("POST /api/v1/posting-strategies", server.createPostingStrategy)
	mux.HandleFunc("GET /api/v1/posting-strategies/{strategyID}", server.getPostingStrategy)
	mux.HandleFunc("PUT /api/v1/posting-strategies/{strategyID}", server.updatePostingStrategy)
	mux.HandleFunc("DELETE /api/v1/posting-strategies/{strategyID}", server.archivePostingStrategy)
	mux.HandleFunc("GET /api/v1/publishing/{taskID}", server.getPublishingDetail)
	mux.HandleFunc("POST /api/v1/publishing/{taskID}/prepare", server.preparePublishing)
	mux.HandleFunc(
		"PUT /api/v1/publishing/{taskID}/platforms/{platform}",
		server.updatePublishingDraft,
	)
	mux.HandleFunc("POST /api/v1/publishing/{taskID}/enqueue", server.enqueuePublishing)
	mux.HandleFunc(
		"POST /api/v1/publishing/{taskID}/platforms/{platform}/retry",
		server.retryPlatformPublishing,
	)
	mux.HandleFunc(
		"POST /api/v1/publishing/{taskID}/platforms/{platform}/resolve",
		server.resolvePlatformPublishing,
	)
	mux.HandleFunc(
		"GET /internal/v1/cookie-profiles/{profileID}/netscape",
		server.getInternalCookieJar,
	)
	mux.HandleFunc(
		"GET /internal/v1/tasks/{taskID}/processing-config",
		server.getInternalProcessingConfig,
	)

	server.handler = server.requestID(
		server.recoverPanic(
			server.securityHeaders(
				server.requestLog(mux),
			),
		),
	)
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) live(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "control-api",
		"version": s.version,
	})
}

func (s *Server) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "database_unavailable", "数据库尚未就绪", nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) systemStatus(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()

	var pendingOutbox int64
	var lastWorkerEvent *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM outbox_messages WHERE status <> 'published'),
			(SELECT max(consumed_at) FROM consumed_messages WHERE consumer='workflow-consumer')
	`).Scan(&pendingOutbox, &lastWorkerEvent)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "system_status_unavailable", "无法读取系统状态", nil)
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"service":            "control-api",
		"version":            s.version,
		"started_at":         s.startedAt,
		"database":           "ready",
		"pending_outbox":     pendingOutbox,
		"last_worker_event":  lastWorkerEvent,
		"message_push_state": "deferred",
	})
}

func (s *Server) dashboard(writer http.ResponseWriter, request *http.Request) {
	summary, err := s.service.Dashboard(request.Context())
	if err != nil {
		s.logger.Error("dashboard query failed", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "dashboard_failed", "无法加载总览", nil)
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}

func (s *Server) fileLibrary(writer http.ResponseWriter, request *http.Request) {
	library, err := s.service.FileLibrary(request.Context())
	if err != nil {
		s.logger.Error("file library query failed", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "file_library_failed", "无法加载文件中心", nil)
		return
	}
	writeJSON(writer, http.StatusOK, library)
}

func (s *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(writer, http.StatusBadRequest, "invalid_limit", "limit 必须是整数", nil)
			return
		}
		limit = value
	}

	scope := strings.TrimSpace(request.URL.Query().Get("scope"))
	items, err := s.service.List(request.Context(), limit, scope)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"任务列表范围无效",
			validationError.Fields,
		)
		return
	}
	if err != nil {
		s.logger.Error("task list failed", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "task_list_failed", "无法加载任务列表", nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getTask(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("taskID")
	task, err := s.service.Get(request.Context(), taskID)
	if errors.Is(err, tasks.ErrInvalidID) {
		writeProblem(writer, http.StatusBadRequest, "invalid_task_id", "任务 ID 格式无效", nil)
		return
	}
	if errors.Is(err, tasks.ErrNotFound) {
		writeProblem(writer, http.StatusNotFound, "task_not_found", "任务不存在", nil)
		return
	}
	if err != nil {
		s.logger.Error("task detail failed", "task_id", taskID, "error", err)
		writeProblem(writer, http.StatusInternalServerError, "task_detail_failed", "无法加载任务详情", nil)
		return
	}
	writeJSON(writer, http.StatusOK, task)
}

func (s *Server) cancelTask(writer http.ResponseWriter, request *http.Request) {
	task, err := s.service.Cancel(request.Context(), request.PathValue("taskID"))
	if s.writeTaskActionError(writer, request, err, "task_cancel_failed", "任务取消失败") {
		return
	}
	writeJSON(writer, http.StatusOK, task)
}

func (s *Server) retryTask(writer http.ResponseWriter, request *http.Request) {
	task, err := s.service.Retry(request.Context(), request.PathValue("taskID"))
	if s.writeTaskActionError(writer, request, err, "task_retry_failed", "任务重试失败") {
		return
	}
	writeJSON(writer, http.StatusOK, task)
}

func (s *Server) setTaskCookieProfile(writer http.ResponseWriter, request *http.Request) {
	var input tasks.SetCookieProfileInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	task, err := s.service.SetCookieProfile(
		request.Context(),
		request.PathValue("taskID"),
		input,
	)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请选择可用的 Cookie 配置",
			validationError.Fields,
		)
		return
	}
	if s.writeTaskActionError(
		writer,
		request,
		err,
		"task_cookie_profile_failed",
		"Cookie 配置更新失败",
	) {
		return
	}
	writeJSON(writer, http.StatusOK, task)
}

func (s *Server) bulkRetryTasks(writer http.ResponseWriter, request *http.Request) {
	var input tasks.BulkRetryInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	result, err := s.service.RetryMany(request.Context(), input)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请选择要重试的失败任务",
			validationError.Fields,
		)
		return
	}
	if err != nil {
		s.logger.Error("bulk retry failed", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "bulk_retry_failed", "批量重试失败", nil)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) deleteTaskAssets(writer http.ResponseWriter, request *http.Request) {
	task, err := s.service.DeleteAssets(request.Context(), request.PathValue("taskID"))
	if s.writeTaskActionError(writer, request, err, "asset_cleanup_failed", "媒体文件清理失败") {
		return
	}
	writeJSON(writer, http.StatusAccepted, task)
}

func (s *Server) taskArchivePreview(
	writer http.ResponseWriter,
	request *http.Request,
) {
	preview, err := s.service.ArchivePreview(request.Context())
	if err != nil {
		s.logger.Error("task archive preview failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"task_archive_preview_failed",
			"无法计算清空任务的影响范围",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, preview)
}

func (s *Server) archiveTask(writer http.ResponseWriter, request *http.Request) {
	var input tasks.ArchiveInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	task, err := s.service.Archive(
		request.Context(),
		request.PathValue("taskID"),
		input,
	)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查任务删除信息",
			validationError.Fields,
		)
		return
	}
	if s.writeTaskActionError(
		writer,
		request,
		err,
		"task_archive_failed",
		"任务未能移入回收站",
	) {
		return
	}
	writeJSON(writer, http.StatusAccepted, task)
}

func (s *Server) archiveAllTasks(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input tasks.ArchiveAllInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	result, err := s.service.ArchiveAll(request.Context(), input)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请重新确认清空范围",
			validationError.Fields,
		)
		return
	}
	var conflict *tasks.ConflictError
	if errors.As(err, &conflict) {
		writeProblem(writer, http.StatusConflict, conflict.Code, conflict.Message, nil)
		return
	}
	if err != nil {
		s.logger.Error("archive all tasks failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"task_archive_all_failed",
			"清空任务列表失败",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func (s *Server) restoreTask(writer http.ResponseWriter, request *http.Request) {
	var input tasks.RestoreInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	task, err := s.service.Restore(
		request.Context(),
		request.PathValue("taskID"),
		input,
	)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查恢复信息",
			validationError.Fields,
		)
		return
	}
	if s.writeTaskActionError(
		writer,
		request,
		err,
		"task_restore_failed",
		"任务恢复失败",
	) {
		return
	}
	writeJSON(writer, http.StatusOK, task)
}

func (s *Server) purgeTask(writer http.ResponseWriter, request *http.Request) {
	var input tasks.PurgeInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	result, err := s.service.Purge(
		request.Context(),
		request.PathValue("taskID"),
		input,
	)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请重新确认永久删除",
			validationError.Fields,
		)
		return
	}
	if s.writeTaskActionError(
		writer,
		request,
		err,
		"task_purge_failed",
		"任务记录永久删除失败",
	) {
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) listCookieProfiles(writer http.ResponseWriter, request *http.Request) {
	items, err := s.cookieService.List(request.Context())
	if err != nil {
		s.logger.Error("cookie profile list failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"cookie_profile_list_failed",
			"无法加载 Cookie 配置",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) uploadCookieProfile(writer http.ResponseWriter, request *http.Request) {
	const maxUploadBytes = 5 << 20
	request.Body = http.MaxBytesReader(writer, request.Body, maxUploadBytes+(1<<20))
	if err := request.ParseMultipartForm(maxUploadBytes + (512 << 10)); err != nil {
		writeProblem(
			writer,
			http.StatusBadRequest,
			"cookie_upload_invalid",
			"Cookie 文件上传无效或超过 5 MiB",
			nil,
		)
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请选择 Cookie 文件",
			map[string]string{"file": "请选择 Netscape 格式 Cookie 文件"},
		)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil || len(content) > maxUploadBytes {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"Cookie 文件无法读取",
			map[string]string{"file": "Cookie 文件不能超过 5 MiB"},
		)
		return
	}
	profile, err := s.cookieService.Upload(
		request.Context(),
		request.FormValue("name"),
		header.Filename,
		content,
	)
	if s.writeCookieProfileError(writer, err) {
		return
	}
	writer.Header().Set("Location", "/api/v1/cookie-profiles/"+profile.ID)
	writeJSON(writer, http.StatusCreated, profile)
}

func (s *Server) createCookieCloudProfile(writer http.ResponseWriter, request *http.Request) {
	var input cookieprofiles.CookieCloudInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	profile, err := s.cookieService.CreateCookieCloud(request.Context(), input)
	if s.writeCookieProfileError(writer, err) {
		return
	}
	writer.Header().Set("Location", "/api/v1/cookie-profiles/"+profile.ID)
	writeJSON(writer, http.StatusCreated, profile)
}

func (s *Server) syncCookieProfile(writer http.ResponseWriter, request *http.Request) {
	profile, err := s.cookieService.Sync(
		request.Context(),
		request.PathValue("profileID"),
	)
	if s.writeCookieProfileError(writer, err) {
		return
	}
	writeJSON(writer, http.StatusOK, profile)
}

func (s *Server) deleteCookieProfile(writer http.ResponseWriter, request *http.Request) {
	err := s.cookieService.Delete(request.Context(), request.PathValue("profileID"))
	if s.writeCookieProfileError(writer, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSettings(writer http.ResponseWriter, request *http.Request) {
	value, err := s.settingsService.Get(request.Context())
	if err != nil {
		s.logger.Error("settings query failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"settings_unavailable",
			"无法读取系统配置",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) updateSettings(writer http.ResponseWriter, request *http.Request) {
	var input appsettings.UpdateInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	value, err := s.settingsService.Update(request.Context(), input)
	var validationError *appsettings.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查系统配置",
			validationError.Fields,
		)
		return
	}
	if errors.Is(err, appsettings.ErrVersionConflict) {
		writeProblem(
			writer,
			http.StatusConflict,
			"settings_version_conflict",
			"配置已被其他操作更新，请刷新后重试",
			nil,
		)
		return
	}
	if err != nil {
		s.logger.Error("settings update failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"settings_update_failed",
			"系统配置保存失败",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) testSettingsConnection(writer http.ResponseWriter, request *http.Request) {
	var input appsettings.ConnectionTestInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	var result appsettings.ConnectionTestResult
	var err error
	if strings.TrimSpace(input.Target) == "moderation" {
		result, err = s.testModerationConnection(request.Context())
	} else {
		result, err = s.settingsService.TestConnection(request.Context(), input)
	}
	var validationError *appsettings.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"connection_not_configured",
			"该服务尚未配置完整",
			validationError.Fields,
		)
		return
	}
	if err != nil {
		s.logger.Warn(
			"settings connection test failed",
			"target", input.Target,
			"error", err,
		)
		writeProblem(
			writer,
			http.StatusBadGateway,
			"connection_test_failed",
			err.Error(),
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) testModerationConnection(
	ctx context.Context,
) (appsettings.ConnectionTestResult, error) {
	started := time.Now()
	current, err := s.settingsService.Get(ctx)
	if err != nil {
		return appsettings.ConnectionTestResult{}, err
	}
	config := current.Moderation
	if config.Provider == "fixture" {
		return appsettings.ConnectionTestResult{
			Target:    "moderation",
			OK:        true,
			Message:   "本地内容安全测试适配器已就绪；结果会明确标记为测试数据",
			LatencyMS: time.Since(started).Milliseconds(),
			CheckedAt: time.Now().UTC(),
			Provider:  "fixture",
		}, nil
	}
	if config.Provider != "aliyun" {
		return appsettings.ConnectionTestResult{}, &appsettings.ValidationError{
			Fields: map[string]string{"target": "未知的内容安全提供商"},
		}
	}
	accessKeyID, err := s.settingsService.ResolveSecret(
		ctx,
		appsettings.SecretAliyunAccessKeyID,
	)
	if err != nil {
		return appsettings.ConnectionTestResult{}, err
	}
	accessKeySecret, err := s.settingsService.ResolveSecret(
		ctx,
		appsettings.SecretAliyunAccessKeySecret,
	)
	if err != nil {
		return appsettings.ConnectionTestResult{}, err
	}
	if strings.TrimSpace(accessKeyID) == "" ||
		strings.TrimSpace(accessKeySecret) == "" {
		return appsettings.ConnectionTestResult{}, &appsettings.ValidationError{
			Fields: map[string]string{
				"target": "尚未保存完整的阿里云 AccessKey ID 和 Secret",
			},
		}
	}
	provider, err := moderation.NewAliyunProvider(
		accessKeyID,
		accessKeySecret,
		config,
	)
	if err != nil {
		return appsettings.ConnectionTestResult{}, err
	}
	testConfig := config
	testConfig.CheckText = true
	testConfig.CheckImage = false
	testConfig.CheckVideo = false
	result, err := provider.Moderate(ctx, moderation.Request{
		TaskID: "connection-test",
		Config: testConfig,
		Texts: []moderation.TextInput{{
			ID:      "connection-test",
			Content: "Visoraft content moderation connection test",
		}},
	})
	if err != nil {
		return appsettings.ConnectionTestResult{}, err
	}
	return appsettings.ConnectionTestResult{
		Target: "moderation",
		OK:     true,
		Message: fmt.Sprintf(
			"阿里云文本审核接口调用成功（本次可能计费），返回风险等级 %s",
			result.RiskLevel,
		),
		LatencyMS: time.Since(started).Milliseconds(),
		CheckedAt: time.Now().UTC(),
		Provider:  "aliyun",
	}, nil
}

func (s *Server) listReviews(writer http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(writer, http.StatusBadRequest, "invalid_limit", "limit 必须是整数", nil)
			return
		}
		limit = value
	}
	items, err := s.reviewService.Queue(request.Context(), limit)
	if err != nil {
		s.logger.Error("review queue query failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"review_queue_failed",
			"无法读取人工审核队列",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getReview(writer http.ResponseWriter, request *http.Request) {
	detail, err := s.reviewService.Detail(request.Context(), request.PathValue("taskID"))
	if s.writeReviewError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) getAssetContent(
	writer http.ResponseWriter,
	request *http.Request,
) {
	taskID := request.PathValue("taskID")
	assetID := request.PathValue("assetID")
	if !identity.IsUUID(taskID) || !identity.IsUUID(assetID) {
		writeProblem(
			writer,
			http.StatusBadRequest,
			"invalid_asset_id",
			"任务或资产 ID 格式无效",
			nil,
		)
		return
	}
	var bucket string
	var objectKey string
	var originalName string
	var contentType string
	err := s.pool.QueryRow(request.Context(), `
		SELECT bucket, object_key, original_name, content_type
		FROM media_assets
		WHERE task_id=$1
		  AND id=$2
		  AND status='available'
		  AND deleted_at IS NULL
	`, taskID, assetID).Scan(
		&bucket,
		&objectKey,
		&originalName,
		&contentType,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(
			writer,
			http.StatusNotFound,
			"asset_not_found",
			"媒体资产不存在或已清理",
			nil,
		)
		return
	}
	if err != nil {
		s.logger.Error(
			"asset lookup failed",
			"task_id", taskID,
			"asset_id", assetID,
			"error", err,
		)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"asset_lookup_failed",
			"无法读取媒体资产",
			nil,
		)
		return
	}

	response, err := s.objectStorage.Get(
		request.Context(),
		bucket,
		objectKey,
		request.Header.Get("Range"),
	)
	if err != nil {
		var upstream *objectstorage.UpstreamError
		if errors.As(err, &upstream) && upstream.StatusCode == http.StatusNotFound {
			writeProblem(
				writer,
				http.StatusNotFound,
				"asset_object_missing",
				"媒体对象不存在",
				nil,
			)
			return
		}
		s.logger.Error(
			"asset stream failed",
			"task_id", taskID,
			"asset_id", assetID,
			"error", err,
		)
		writeProblem(
			writer,
			http.StatusBadGateway,
			"asset_storage_unavailable",
			"对象存储暂时不可用",
			nil,
		)
		return
	}
	defer response.Body.Close()

	if _, _, parseErr := mime.ParseMediaType(contentType); parseErr != nil {
		contentType = "application/octet-stream"
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "private, max-age=60")
	writer.Header().Set("Accept-Ranges", "bytes")
	writer.Header().Set("X-Asset-Name", safeHeaderValue(originalName))
	if value := response.Header.Get("Content-Length"); value != "" {
		writer.Header().Set("Content-Length", value)
	}
	if value := response.Header.Get("Content-Range"); value != "" {
		writer.Header().Set("Content-Range", value)
	}
	if value := response.Header.Get("ETag"); value != "" {
		writer.Header().Set("ETag", value)
	}
	disposition := "attachment"
	if strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") ||
		strings.HasPrefix(contentType, "image/") ||
		contentType == "text/vtt" {
		disposition = "inline"
	}
	writer.Header().Set("Content-Disposition", disposition)
	writer.WriteHeader(response.StatusCode)
	if _, err := io.Copy(writer, response.Body); err != nil {
		s.logger.Warn(
			"asset stream interrupted",
			"task_id", taskID,
			"asset_id", assetID,
			"error", err,
		)
	}
}

func safeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	if len(value) > 240 {
		return value[:240]
	}
	return value
}

func (s *Server) updateReviewMetadata(writer http.ResponseWriter, request *http.Request) {
	var input reviews.MetadataInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	detail, err := s.reviewService.UpdateMetadata(
		request.Context(),
		request.PathValue("taskID"),
		input,
	)
	if s.writeReviewError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) updateReviewSubtitle(writer http.ResponseWriter, request *http.Request) {
	var input reviews.SubtitleInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	detail, err := s.reviewService.UpdateSubtitle(
		request.Context(),
		request.PathValue("taskID"),
		request.PathValue("documentID"),
		input,
	)
	if s.writeReviewError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) actOnReview(writer http.ResponseWriter, request *http.Request) {
	var input reviews.ActionInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	detail, err := s.reviewService.Act(
		request.Context(),
		request.PathValue("taskID"),
		request.PathValue("action"),
		input,
	)
	if s.writeReviewError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) writeReviewError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, tasks.ErrInvalidID) {
		writeProblem(writer, http.StatusBadRequest, "invalid_task_id", "任务 ID 格式无效", nil)
		return true
	}
	if errors.Is(err, tasks.ErrNotFound) {
		writeProblem(writer, http.StatusNotFound, "task_not_found", "任务不存在", nil)
		return true
	}
	var validationError *reviews.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查审核信息",
			validationError.Fields,
		)
		return true
	}
	var conflict *reviews.ConflictError
	if errors.As(err, &conflict) {
		writeProblem(writer, http.StatusConflict, conflict.Code, conflict.Message, nil)
		return true
	}
	s.logger.Error(
		"review action failed",
		"task_id", request.PathValue("taskID"),
		"path", request.URL.Path,
		"error", err,
	)
	writeProblem(
		writer,
		http.StatusInternalServerError,
		"review_action_failed",
		"审核操作失败",
		nil,
	)
	return true
}

func (s *Server) listYouTubeMonitors(
	writer http.ResponseWriter,
	request *http.Request,
) {
	items, err := s.monitorService.List(request.Context())
	if err != nil {
		s.logger.Error("youtube monitor list failed", "error", err)
		writeProblem(
			writer,
			http.StatusInternalServerError,
			"youtube_monitor_list_failed",
			"无法读取 YouTube 监控配置",
			nil,
		)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listYouTubeCategories(
	writer http.ResponseWriter,
	request *http.Request,
) {
	items, err := s.monitorService.YouTubeCategories(
		request.Context(),
		request.URL.Query().Get("region"),
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input monitors.CreateInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.monitorService.Create(request.Context(), input)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writer.Header().Set("Location", "/api/v1/youtube-monitors/"+item.ID)
	writeJSON(writer, http.StatusCreated, item)
}

func (s *Server) getYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	item, err := s.monitorService.Get(
		request.Context(),
		request.PathValue("monitorID"),
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) updateYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input monitors.UpdateInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.monitorService.Update(
		request.Context(),
		request.PathValue("monitorID"),
		input,
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) deleteYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input monitors.DeleteInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	err := s.monitorService.Delete(
		request.Context(),
		request.PathValue("monitorID"),
		input,
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) pauseYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	item, err := s.monitorService.Pause(
		request.Context(),
		request.PathValue("monitorID"),
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) resumeYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	item, err := s.monitorService.Resume(
		request.Context(),
		request.PathValue("monitorID"),
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) runYouTubeMonitor(
	writer http.ResponseWriter,
	request *http.Request,
) {
	run, err := s.monitorService.RunNow(
		request.Context(),
		request.PathValue("monitorID"),
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusAccepted, run)
}

func (s *Server) youtubeMonitorHistory(
	writer http.ResponseWriter,
	request *http.Request,
) {
	history, err := s.monitorService.History(
		request.Context(),
		request.PathValue("monitorID"),
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, history)
}

func (s *Server) enqueueYouTubeMonitorItems(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input monitors.EnqueueItemsInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	result, err := s.monitorService.EnqueueItems(
		request.Context(),
		request.PathValue("monitorID"),
		input,
	)
	if s.writeMonitorError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) writeMonitorError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, tasks.ErrInvalidID) {
		writeProblem(writer, http.StatusBadRequest, "invalid_monitor_id", "监控 ID 格式无效", nil)
		return true
	}
	if errors.Is(err, monitors.ErrNotFound) {
		writeProblem(writer, http.StatusNotFound, "youtube_monitor_not_found", "监控配置不存在", nil)
		return true
	}
	if errors.Is(err, monitors.ErrVersionConflict) {
		writeProblem(
			writer,
			http.StatusConflict,
			"youtube_monitor_version_conflict",
			"监控配置已更新，请刷新后重试",
			nil,
		)
		return true
	}
	var validationError *monitors.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查监控配置",
			validationError.Fields,
		)
		return true
	}
	var conflict *monitors.ConflictError
	if errors.As(err, &conflict) {
		writeProblem(writer, http.StatusConflict, conflict.Code, conflict.Message, nil)
		return true
	}
	s.logger.Error(
		"youtube monitor action failed",
		"monitor_id", request.PathValue("monitorID"),
		"path", request.URL.Path,
		"error", err,
	)
	writeProblem(
		writer,
		http.StatusInternalServerError,
		"youtube_monitor_action_failed",
		"YouTube 监控操作失败",
		nil,
	)
	return true
}

func (s *Server) getInternalProcessingConfig(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !s.isWorkerAuthorized(request) {
		writeProblem(
			writer,
			http.StatusUnauthorized,
			"worker_unauthorized",
			"Worker 凭据无效",
			nil,
		)
		return
	}
	value, err := s.settingsService.ProcessingConfig(
		request.Context(),
		request.PathValue("taskID"),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(writer, http.StatusNotFound, "task_not_found", "任务不存在", nil)
		return
	}
	if err != nil {
		s.logger.Error(
			"internal processing config failed",
			"task_id", request.PathValue("taskID"),
			"error", err,
		)
		writeProblem(
			writer,
			http.StatusConflict,
			"processing_config_unavailable",
			"任务处理配置不可用",
			nil,
		)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) getInternalCookieJar(writer http.ResponseWriter, request *http.Request) {
	if !s.isWorkerAuthorized(request) {
		writeProblem(
			writer,
			http.StatusUnauthorized,
			"worker_unauthorized",
			"Worker 凭据无效",
			nil,
		)
		return
	}
	content, err := s.cookieService.CookieJar(
		request.Context(),
		request.PathValue("profileID"),
	)
	if errors.Is(err, cookieprofiles.ErrNotFound) {
		writeProblem(
			writer,
			http.StatusNotFound,
			"cookie_profile_not_found",
			"Cookie 配置不存在",
			nil,
		)
		return
	}
	if err != nil {
		s.logger.Error(
			"internal cookie material failed",
			"profile_id", request.PathValue("profileID"),
			"error", err,
		)
		writeProblem(
			writer,
			http.StatusConflict,
			"cookie_profile_unavailable",
			"Cookie 配置尚无可用内容",
			nil,
		)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Disposition", `attachment; filename="cookies.txt"`)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(content)
}

func (s *Server) writeCookieProfileError(writer http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var validationError *cookieprofiles.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查 Cookie 配置",
			validationError.Fields,
		)
		return true
	}
	if errors.Is(err, cookieprofiles.ErrNotFound) {
		writeProblem(
			writer,
			http.StatusNotFound,
			"cookie_profile_not_found",
			"Cookie 配置不存在",
			nil,
		)
		return true
	}
	s.logger.Error("cookie profile action failed", "error", err)
	writeProblem(
		writer,
		http.StatusInternalServerError,
		"cookie_profile_action_failed",
		"Cookie 配置操作失败",
		nil,
	)
	return true
}

func (s *Server) isWorkerAuthorized(request *http.Request) bool {
	const prefix = "Bearer "
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := []byte(strings.TrimSpace(strings.TrimPrefix(value, prefix)))
	expected := []byte(s.workerToken)
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(provided, expected) == 1
}

func (s *Server) writeTaskActionError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
	fallbackCode string,
	fallbackMessage string,
) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, tasks.ErrInvalidID) {
		writeProblem(writer, http.StatusBadRequest, "invalid_task_id", "任务 ID 格式无效", nil)
		return true
	}
	if errors.Is(err, tasks.ErrNotFound) {
		writeProblem(writer, http.StatusNotFound, "task_not_found", "任务不存在", nil)
		return true
	}
	var conflict *tasks.ConflictError
	if errors.As(err, &conflict) {
		writeProblem(writer, http.StatusConflict, conflict.Code, conflict.Message, nil)
		return true
	}
	s.logger.Error(
		"task action failed",
		"task_id", request.PathValue("taskID"),
		"path", request.URL.Path,
		"error", err,
	)
	writeProblem(writer, http.StatusInternalServerError, fallbackCode, fallbackMessage, nil)
	return true
}

func (s *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var input tasks.CreateInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}

	task, err := s.service.Create(request.Context(), input)
	var validationError *tasks.ValidationError
	if errors.As(err, &validationError) {
		writeProblem(writer, http.StatusUnprocessableEntity, "validation_failed", "请检查建单信息", validationError.Fields)
		return
	}
	if err != nil {
		s.logger.Error("create task failed", "error", err)
		writeProblem(writer, http.StatusInternalServerError, "task_create_failed", "任务创建失败", nil)
		return
	}

	writer.Header().Set("Location", "/api/v1/tasks/"+task.ID)
	writeJSON(writer, http.StatusCreated, task)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求 JSON 无效：%w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(writer http.ResponseWriter, status int, code, message string, fields map[string]string) {
	writeJSON(writer, status, errorBody{Error: problem{
		Code:    code,
		Message: message,
		Fields:  fields,
	}})
}

type contextKey string

const requestIDKey contextKey = "request-id"

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			generated, err := identity.NewUUID()
			if err != nil {
				generated = fmt.Sprintf("fallback-%d", time.Now().UnixNano())
			}
			requestID = generated
		}
		writer.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		s.logger.Info(
			"http request",
			"request_id", request.Context().Value(requestIDKey),
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error(
					"http panic recovered",
					"request_id", request.Context().Value(requestIDKey),
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				writeProblem(writer, http.StatusInternalServerError, "internal_error", "系统内部错误", nil)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
