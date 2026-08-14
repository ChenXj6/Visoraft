package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/visoraft/visoraft/internal/publishing"
)

func (s *Server) listPlatformAccounts(
	writer http.ResponseWriter,
	request *http.Request,
) {
	items, err := s.publishService.ListAccounts(
		request.Context(),
		request.URL.Query().Get("platform"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPlatformAccount(
	writer http.ResponseWriter,
	request *http.Request,
) {
	item, err := s.publishService.GetAccount(
		request.Context(),
		request.PathValue("accountID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) createPlatformAccount(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.CreateAccountInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.publishService.CreateAccount(request.Context(), input)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writer.Header().Set("Location", "/api/v1/platform-accounts/"+item.ID)
	writeJSON(writer, http.StatusCreated, item)
}

func (s *Server) updatePlatformAccount(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.UpdateAccountInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.publishService.UpdateAccount(
		request.Context(),
		request.PathValue("accountID"),
		input,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) archivePlatformAccount(
	writer http.ResponseWriter,
	request *http.Request,
) {
	version, err := expectedVersion(request)
	if err != nil {
		writeProblem(
			writer,
			http.StatusBadRequest,
			"invalid_expected_version",
			"expected_version 必须是正整数",
			nil,
		)
		return
	}
	err = s.publishService.ArchiveAccount(
		request.Context(),
		request.PathValue("accountID"),
		version,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) checkPlatformAccount(
	writer http.ResponseWriter,
	request *http.Request,
) {
	result, err := s.publishService.CheckAccount(
		request.Context(),
		request.PathValue("accountID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) listPlatformCategories(
	writer http.ResponseWriter,
	request *http.Request,
) {
	items, err := s.publishService.ListCategories(
		request.Context(),
		request.URL.Query().Get("platform"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) refreshPlatformCategories(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input struct {
		AccountID string `json:"account_id"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	items, err := s.publishService.RefreshCategories(
		request.Context(),
		strings.TrimSpace(input.AccountID),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listTranscodePresets(
	writer http.ResponseWriter,
	request *http.Request,
) {
	items, err := s.publishService.ListTranscodePresets(request.Context())
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getTranscodePreset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	item, err := s.publishService.GetTranscodePreset(
		request.Context(),
		request.PathValue("presetID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) createTranscodePreset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.TranscodePresetInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.publishService.CreateTranscodePreset(request.Context(), input)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writer.Header().Set("Location", "/api/v1/transcode-presets/"+item.ID)
	writeJSON(writer, http.StatusCreated, item)
}

func (s *Server) updateTranscodePreset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.UpdateTranscodePresetInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.publishService.UpdateTranscodePreset(
		request.Context(),
		request.PathValue("presetID"),
		input,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) archiveTranscodePreset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	version, err := expectedVersion(request)
	if err != nil {
		writeProblem(
			writer,
			http.StatusBadRequest,
			"invalid_expected_version",
			"expected_version 必须是正整数",
			nil,
		)
		return
	}
	err = s.publishService.ArchiveTranscodePreset(
		request.Context(),
		request.PathValue("presetID"),
		version,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPostingStrategies(
	writer http.ResponseWriter,
	request *http.Request,
) {
	items, err := s.publishService.ListPostingStrategies(request.Context())
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPostingStrategy(
	writer http.ResponseWriter,
	request *http.Request,
) {
	item, err := s.publishService.GetPostingStrategy(
		request.Context(),
		request.PathValue("strategyID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) createPostingStrategy(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.PostingStrategyInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.publishService.CreatePostingStrategy(request.Context(), input)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writer.Header().Set("Location", "/api/v1/posting-strategies/"+item.ID)
	writeJSON(writer, http.StatusCreated, item)
}

func (s *Server) updatePostingStrategy(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.UpdatePostingStrategyInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item, err := s.publishService.UpdatePostingStrategy(
		request.Context(),
		request.PathValue("strategyID"),
		input,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (s *Server) archivePostingStrategy(
	writer http.ResponseWriter,
	request *http.Request,
) {
	version, err := expectedVersion(request)
	if err != nil {
		writeProblem(
			writer,
			http.StatusBadRequest,
			"invalid_expected_version",
			"expected_version 必须是正整数",
			nil,
		)
		return
	}
	err = s.publishService.ArchivePostingStrategy(
		request.Context(),
		request.PathValue("strategyID"),
		version,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPublishingDetail(
	writer http.ResponseWriter,
	request *http.Request,
) {
	detail, err := s.publishService.Detail(
		request.Context(),
		request.PathValue("taskID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) preparePublishing(
	writer http.ResponseWriter,
	request *http.Request,
) {
	detail, err := s.publishService.Prepare(
		request.Context(),
		request.PathValue("taskID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) updatePublishingDraft(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.DraftPlatformInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	detail, err := s.publishService.UpdateDraft(
		request.Context(),
		request.PathValue("taskID"),
		request.PathValue("platform"),
		input,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) enqueuePublishing(
	writer http.ResponseWriter,
	request *http.Request,
) {
	detail, err := s.publishService.Enqueue(
		request.Context(),
		request.PathValue("taskID"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusAccepted, detail)
}

func (s *Server) retryPlatformPublishing(
	writer http.ResponseWriter,
	request *http.Request,
) {
	detail, err := s.publishService.RetryPlatform(
		request.Context(),
		request.PathValue("taskID"),
		request.PathValue("platform"),
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusAccepted, detail)
}

func (s *Server) resolvePlatformPublishing(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input publishing.ResolvePublicationInput
	if err := decodeJSON(writer, request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	detail, err := s.publishService.ResolvePlatform(
		request.Context(),
		request.PathValue("taskID"),
		request.PathValue("platform"),
		input,
	)
	if s.writePublishingError(writer, request, err) {
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (s *Server) writePublishingError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, publishing.ErrNotFound) {
		writeProblem(
			writer,
			http.StatusNotFound,
			"publishing_resource_not_found",
			"投稿资源不存在",
			nil,
		)
		return true
	}
	if errors.Is(err, publishing.ErrVersionConflict) {
		writeProblem(
			writer,
			http.StatusConflict,
			"publishing_version_conflict",
			"数据已经更新，请刷新后重试",
			nil,
		)
		return true
	}
	var validation *publishing.ValidationError
	if errors.As(err, &validation) {
		writeProblem(
			writer,
			http.StatusUnprocessableEntity,
			"validation_failed",
			"请检查投稿配置",
			validation.Fields,
		)
		return true
	}
	var conflict *publishing.ConflictError
	if errors.As(err, &conflict) {
		writeProblem(writer, http.StatusConflict, conflict.Code, conflict.Message, nil)
		return true
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505":
			writeProblem(
				writer,
				http.StatusConflict,
				"publishing_name_conflict",
				"同名配置已经存在",
				nil,
			)
			return true
		case "23503", "23514":
			writeProblem(
				writer,
				http.StatusUnprocessableEntity,
				"publishing_reference_invalid",
				"关联的账号、分区、转码预设或投稿策略不可用",
				nil,
			)
			return true
		}
	}
	s.logger.Error(
		"publishing action failed",
		"path", request.URL.Path,
		"error", err,
	)
	writeProblem(
		writer,
		http.StatusInternalServerError,
		"publishing_action_failed",
		"投稿操作失败",
		nil,
	)
	return true
}

func expectedVersion(request *http.Request) (int64, error) {
	value, err := strconv.ParseInt(
		strings.TrimSpace(request.URL.Query().Get("expected_version")),
		10,
		64,
	)
	if err != nil || value < 1 {
		return 0, errors.New("invalid expected version")
	}
	return value, nil
}
