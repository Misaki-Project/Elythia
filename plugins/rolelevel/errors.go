package rolelevel

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/shiroha-a/mk/plugin"
)

// Stable API error codes.
//
// **frontend がここで分岐する。** code をリネームすると Misaki 側の frontend plugin が
// 黙って壊れるので公開契約として扱う。prefix 以外の変更は破壊的。
const (
	CodeUnauthenticated  = "ROLE_LEVEL_UNAUTHENTICATED"
	CodeForbidden        = "ROLE_LEVEL_FORBIDDEN"
	CodeValidationFailed = "ROLE_LEVEL_VALIDATION_FAILED"

	CodeConfigNotFound       = "ROLE_LEVEL_CONFIG_NOT_FOUND"
	CodeConfigConflict       = "ROLE_LEVEL_CONFIG_CONFLICT"
	CodeInvalidBaseLevel     = "ROLE_LEVEL_INVALID_BASE_LEVEL"
	CodeInvalidCurve         = "ROLE_LEVEL_INVALID_CURVE"
	CodeInvalidRanges        = "ROLE_LEVEL_INVALID_RANGES"
	CodeUnknownPolicyKey     = "ROLE_LEVEL_UNKNOWN_POLICY_KEY"
	CodeMultiplierNotNumeric = "ROLE_LEVEL_MULTIPLIER_NOT_NUMERIC"
	CodeInvalidRangeValue    = "ROLE_LEVEL_INVALID_RANGE_VALUE"

	CodeRoleNotManual      = "ROLE_LEVEL_ROLE_NOT_MANUAL"
	CodeRoleNotAssignable  = "ROLE_LEVEL_ROLE_NOT_ASSIGNABLE"
	CodeActorNotConfigured = "ROLE_LEVEL_ACTOR_NOT_CONFIGURED"

	CodeUnknownMode            = "ROLE_LEVEL_UNKNOWN_MODE"
	CodeOperandOutOfRange      = "ROLE_LEVEL_OPERAND_OUT_OF_RANGE"
	CodeIdempotencyKeyRequired = "ROLE_LEVEL_IDEMPOTENCY_KEY_REQUIRED"
	CodeIdempotencyKeyInvalid  = "ROLE_LEVEL_IDEMPOTENCY_KEY_INVALID"

	CodeAssignmentUnresolved    = "ROLE_LEVEL_ASSIGNMENT_UNRESOLVED"
	CodeAssignmentScanExhausted = "ROLE_LEVEL_ASSIGNMENT_SCAN_EXHAUSTED"
	CodeNativeRoleNotFound      = "ROLE_LEVEL_NATIVE_ROLE_NOT_FOUND"
	CodeNativeAPIFailed         = "ROLE_LEVEL_NATIVE_API_FAILED"
	CodeStorageFailed           = "ROLE_LEVEL_STORAGE_FAILED"
	// CodeOrphanScanExhausted reports an XP-row walk that hit its page cap, so the
	// plugin cannot tell live rows from orphans for that role.
	CodeOrphanScanExhausted = "ROLE_LEVEL_ORPHAN_SCAN_EXHAUSTED"

	// CodeIdempotencyConflict reports a retry that reuses an idempotency key for a
	// different request payload.
	CodeIdempotencyConflict = "ROLE_LEVEL_IDEMPOTENCY_CONFLICT"
	// CodeOperationFailed reports a retry of an operation that already reached failed.
	CodeOperationFailed = "ROLE_LEVEL_OPERATION_FAILED"
)

// ValidationError is a configuration rejection that already knows which stable
// code it should surface as.
//
// **domain層はHTTPを知らない。** 同じ検査を route / reconciliation job / unit test から
// 呼ぶので、domainはcode付きの型を返し、routeだけがstatusを付ける。
type ValidationError struct {
	Code  string
	Field string
	Err   error
}

// Error implements error.
func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Err.Error()
	}
	return e.Field + ": " + e.Err.Error()
}

// Unwrap implements the error chain contract.
func (e *ValidationError) Unwrap() error { return e.Err }

func invalid(code, field, format string, args ...any) error {
	return &ValidationError{Code: code, Field: field, Err: fmt.Errorf(format, args...)}
}

// codedErrorf builds a coded status error directly, for failures that are not
// configuration rejections (authorization, native API, storage).
//
// 素のerrorを返すとhostは 500 + "Internal error." に丸め、frontendはvalidation事由と
// 障害を区別できなくなる。
func codedErrorf(status int, code, format string, args ...any) error {
	return plugin.NewCodedStatusError(status, fmt.Sprintf(format, args...), code)
}

// statusError converts a domain validation failure into a coded status error and
// leaves every other error alone (so the host logs it and answers 500).
func statusError(err error) error {
	if err == nil {
		return nil
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		return plugin.NewCodedStatusError(http.StatusBadRequest, ve.Error(), ve.Code)
	}
	return err
}

// idRe bounds an opaque native id the plugin accepts. **mk-go の id 形式は解釈しない**
// (aidx / ulid / uuid のどれが来てもそのまま扱う)。禁じるのはログ行やエラーメッセージで
// 引用が必要になる文字だけ。
var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validateID(field, value string) error {
	if value == "" {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed, "%s が必要です", field)
	}
	if !idRe.MatchString(value) {
		return codedErrorf(http.StatusBadRequest, CodeValidationFailed,
			"%s の形式が不正です (英数字とハイフン・アンダースコアのみ、64文字まで)", field)
	}
	return nil
}
