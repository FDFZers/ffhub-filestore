package errs

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ErrorInfo struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

type Error struct {
	HTTPCode int
	Info     ErrorInfo
}

func CreateError(httpCode int, id string) *Error {
	return &Error{
		HTTPCode: httpCode,
		Info: ErrorInfo{
			Error: id,
		},
	}
}

func (e *Error) AppendDetails(detail ...string) *Error {
	e.Info.Details = append(e.Info.Details, detail...)
	return e
}

func (e *Error) Respond(c *gin.Context) {
	c.JSON(e.HTTPCode, e.Info)
}

func InternalError() *Error {
	return CreateError(http.StatusInternalServerError, "internal_error")
}
func BadRequestError() *Error {
	return CreateError(http.StatusBadRequest, "bad_request")
}
func UnauthorizedError() *Error {
	return CreateError(http.StatusUnauthorized, "unauthorized")
}
func NotFoundError() *Error {
	return CreateError(http.StatusNotFound, "not_found")
}
func ConflictError() *Error {
	return CreateError(http.StatusConflict, "conflict")
}
func ForbiddenError() *Error {
	return CreateError(http.StatusForbidden, "forbidden")
}

func CreateAndLogInternalError[T any](err T, log string, args ...any) *Error {
	slog.Error(log, append([]any{"error", err}, args...)...)
	return InternalError()
}

func CreatePGError(err error, item, log string) *Error {
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFoundError().AppendDetails(fmt.Sprintf("“%s”不存在", item))
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !strings.HasPrefix(pgErr.Code, "23") {
		return CreateAndLogInternalError(err, log).AppendDetails("数据库内部错误")
	}

	return ConflictError().AppendDetails(fmt.Sprintf("“%s”冲突", pgErr.ConstraintName))
}
