package monitor

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// 错误码常量（api-contract §2.1）。
const (
	CodeInvalidRequest  = "invalid_request"
	CodeUnauthorized    = "unauthorized"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeValidationError = "validation_error"
	CodeInternalError   = "internal_error"
)

// APIError 统一错误体（api-contract §2.1）。
type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
	TraceID string      `json:"traceId,omitempty"`
}

// apiError 是内部带 HTTP 状态码的错误，可被 handler 包装并统一响应。
type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func newAPIError(status int, code, msg string) *apiError {
	return &apiError{status: status, code: code, msg: msg}
}

// 预定义错误（供 handler 复用）。
var (
	errBadRequest     = newAPIError(http.StatusBadRequest, CodeInvalidRequest, "请求格式错误")
	errUnauthorized   = newAPIError(http.StatusUnauthorized, CodeUnauthorized, "未授权，请先登录")
	errForbidden      = newAPIError(http.StatusForbidden, CodeForbidden, "权限不足")
	errNotFoundAPI    = newAPIError(http.StatusNotFound, CodeNotFound, "资源不存在")
	errConflictAPI    = newAPIError(http.StatusConflict, CodeConflict, "资源冲突")
	errValidation     = newAPIError(http.StatusUnprocessableEntity, CodeValidationError, "业务校验失败")
	errInternal       = newAPIError(http.StatusInternalServerError, CodeInternalError, "内部错误")
	errNotImplemented = newAPIError(http.StatusNotImplemented, CodeInternalError, "端点尚未实现")
)

// respondError 写出统一错误体，traceId 取自 requestID 中间件。
func respondError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIError{
		Code:    code,
		Message: msg,
		TraceID: middleware.GetReqID(r.Context()),
	})
}

// respondAPIError 用 apiError 响应。
func respondAPIError(w http.ResponseWriter, r *http.Request, e *apiError) {
	respondError(w, r, e.status, e.code, e.msg)
}
