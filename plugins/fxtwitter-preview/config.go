// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"fmt"
	"time"

	"github.com/elythia-network/elythia/plugin"
)

type config struct {
	ExternalFetchEnabled     bool
	RefetchInterval          time.Duration
	AuditRetention           time.Duration
	RequestTimeout           time.Duration
	MaxResponseBytes         int
	StaticImageMaxBytes      int
	AuthenticatedBudget      int
	AuthenticatedDailyBudget int
	AnonymousBudget          int
	GlobalBudget             int
	RoleBudgets              map[string]coldFetchBudget
	VideoPlaybackMode        string
}

type coldFetchBudget struct {
	PerHour int
	PerDay  int
}

type rawRoleColdFetchLimit struct {
	PerHour int `json:"perHour"`
	PerDay  int `json:"perDay"`
}

type rawConfig struct {
	ExternalFetchEnabled            bool                             `json:"externalFetchEnabled"`
	RefetchIntervalMinutes          int                              `json:"refetchIntervalMinutes"`
	AuditRetentionDays              int                              `json:"auditRetentionDays"`
	RequestTimeoutSeconds           int                              `json:"requestTimeoutSeconds"`
	MaxResponseBytes                int                              `json:"maxResponseBytes"`
	StaticImageMaxBytes             int                              `json:"staticImageMaxBytes"`
	AuthenticatedColdFetchesPerHour int                              `json:"authenticatedColdFetchesPerHour"`
	AuthenticatedColdFetchesPerDay  int                              `json:"authenticatedColdFetchesPerDay"`
	AnonymousColdFetchesPerHour     int                              `json:"anonymousColdFetchesPerHour"`
	GlobalColdFetchesPerHour        int                              `json:"globalColdFetchesPerHour"`
	RoleColdFetchLimits             map[string]rawRoleColdFetchLimit `json:"roleColdFetchLimits"`
	VideoPlaybackMode               string                           `json:"videoPlaybackMode"`
}

func loadConfig(ctx plugin.Context) (config, error) {
	raw := rawConfig{
		RefetchIntervalMinutes:          60,
		AuditRetentionDays:              0,
		RequestTimeoutSeconds:           5,
		MaxResponseBytes:                1 << 20,
		StaticImageMaxBytes:             10 << 20,
		AuthenticatedColdFetchesPerHour: 100,
		AuthenticatedColdFetchesPerDay:  600,
		AnonymousColdFetchesPerHour:     60,
		GlobalColdFetchesPerHour:        300,
		VideoPlaybackMode:               "officialEmbed",
	}
	if err := ctx.Config().Unmarshal(&raw); err != nil {
		return config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if raw.RefetchIntervalMinutes < 1 || raw.RefetchIntervalMinutes > 7*24*60 {
		return config{}, fmt.Errorf("refetchIntervalMinutes must be between 1 and 10080")
	}
	if raw.AuditRetentionDays < 0 || raw.AuditRetentionDays > 36500 {
		return config{}, fmt.Errorf("auditRetentionDays must be between 0 and 36500")
	}
	if raw.RequestTimeoutSeconds < 1 || raw.RequestTimeoutSeconds > 30 {
		return config{}, fmt.Errorf("requestTimeoutSeconds must be between 1 and 30")
	}
	if raw.MaxResponseBytes < 1024 || raw.MaxResponseBytes > 4<<20 {
		return config{}, fmt.Errorf("maxResponseBytes must be between 1024 and 4194304")
	}
	if raw.StaticImageMaxBytes < 1024 || raw.StaticImageMaxBytes > 50<<20 {
		return config{}, fmt.Errorf("staticImageMaxBytes must be between 1024 and 52428800")
	}
	if raw.AuthenticatedColdFetchesPerHour < 1 || raw.AuthenticatedColdFetchesPerHour > 10000 {
		return config{}, fmt.Errorf("authenticatedColdFetchesPerHour must be between 1 and 10000")
	}
	if raw.AuthenticatedColdFetchesPerDay < 1 || raw.AuthenticatedColdFetchesPerDay > 100000 {
		return config{}, fmt.Errorf("authenticatedColdFetchesPerDay must be between 1 and 100000")
	}
	if raw.AnonymousColdFetchesPerHour < 1 || raw.AnonymousColdFetchesPerHour > 10000 {
		return config{}, fmt.Errorf("anonymousColdFetchesPerHour must be between 1 and 10000")
	}
	if raw.GlobalColdFetchesPerHour < 1 || raw.GlobalColdFetchesPerHour > 100000 {
		return config{}, fmt.Errorf("globalColdFetchesPerHour must be between 1 and 100000")
	}
	if raw.VideoPlaybackMode != "officialEmbed" && raw.VideoPlaybackMode != "linkOnly" {
		return config{}, fmt.Errorf("videoPlaybackMode must be officialEmbed or linkOnly")
	}
	roleBudgets := make(map[string]coldFetchBudget, len(raw.RoleColdFetchLimits))
	for roleID, limits := range raw.RoleColdFetchLimits {
		if len(roleID) == 0 || len(roleID) > 32 {
			return config{}, fmt.Errorf("roleColdFetchLimits contains an invalid role ID")
		}
		if limits.PerHour < 1 || limits.PerHour > 10000 || limits.PerDay < 1 || limits.PerDay > 100000 {
			return config{}, fmt.Errorf("roleColdFetchLimits[%q] must provide perHour 1-10000 and perDay 1-100000", roleID)
		}
		roleBudgets[roleID] = coldFetchBudget{PerHour: limits.PerHour, PerDay: limits.PerDay}
	}
	return config{
		ExternalFetchEnabled:     raw.ExternalFetchEnabled,
		RefetchInterval:          time.Duration(raw.RefetchIntervalMinutes) * time.Minute,
		AuditRetention:           time.Duration(raw.AuditRetentionDays) * 24 * time.Hour,
		RequestTimeout:           time.Duration(raw.RequestTimeoutSeconds) * time.Second,
		MaxResponseBytes:         raw.MaxResponseBytes,
		StaticImageMaxBytes:      raw.StaticImageMaxBytes,
		AuthenticatedBudget:      raw.AuthenticatedColdFetchesPerHour,
		AuthenticatedDailyBudget: raw.AuthenticatedColdFetchesPerDay,
		AnonymousBudget:          raw.AnonymousColdFetchesPerHour,
		GlobalBudget:             raw.GlobalColdFetchesPerHour,
		RoleBudgets:              roleBudgets,
		VideoPlaybackMode:        raw.VideoPlaybackMode,
	}, nil
}

func (c config) effectiveBudget(roleIDs []string) coldFetchBudget {
	result := coldFetchBudget{PerHour: c.AuthenticatedBudget, PerDay: c.AuthenticatedDailyBudget}
	for _, roleID := range roleIDs {
		limits, ok := c.RoleBudgets[roleID]
		if !ok {
			continue
		}
		if limits.PerHour > result.PerHour {
			result.PerHour = limits.PerHour
		}
		if limits.PerDay > result.PerDay {
			result.PerDay = limits.PerDay
		}
	}
	return result
}
