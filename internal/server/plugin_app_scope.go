package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/elythia-network/elythia/internal/misc/permissions"
	"github.com/elythia-network/elythia/internal/server/middleware"
)

// pluginAppTokenPolicy keeps app tokens out of plugin routes by default.
// MisakiのXP変更だけを専用scopeで許可する。ロール設定・他plugin・別HTTP methodは開放しない。
// 実際の管理者/モデレーター・対象ロールの認可はrole-levelのhandlerで引き続き行う。
func pluginAppTokenPolicy() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		scoped := middleware.RequireScope(permissions.WriteRoleLevelExperience)(next)
		denied := middleware.RejectAppToken()(next)
		return func(c echo.Context) error {
			if c.Request().Method == http.MethodPost && c.Path() == "/api/plugin/role-level/admin/change-exp" {
				return scoped(c)
			}
			return denied(c)
		}
	}
}
