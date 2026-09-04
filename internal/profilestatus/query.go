package profilestatus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"

	"codex-manage/internal/codexapp"
	profilemgr "codex-manage/internal/profiles"
)

var ErrSignInRequired = errors.New("sign-in required")

type Fetcher interface {
	Fetch(context.Context, profilemgr.ProfileSummary) (profilemgr.ProfileStatus, error)
}

type Queryer struct {
	manager    profilemgr.Manager
	executable string
	resolveErr error
	launcher   codexapp.Launcher
}

func New(manager profilemgr.Manager) *Queryer {
	executable, err := exec.LookPath("codex")
	return &Queryer{manager: manager, executable: executable, resolveErr: err, launcher: codexapp.DefaultLauncher()}
}

func (q *Queryer) Fetch(ctx context.Context, profile profilemgr.ProfileSummary) (profilemgr.ProfileStatus, error) {
	if profile.Kind != profilemgr.AuthKindChatGPT {
		return profilemgr.ProfileStatus{}, errors.New("status is available only for ChatGPT profiles")
	}
	if q.resolveErr != nil {
		return profilemgr.ProfileStatus{}, errors.New("codex CLI was not found on PATH")
	}
	source, err := q.manager.StatusQuerySource(profile.Key)
	if err != nil {
		return profilemgr.ProfileStatus{}, err
	}
	session, err := q.launcher.Start(ctx, q.executable, codexapp.Seed{
		Prefix: "codex-manage-status-*", AuthJSON: source.AuthJSON,
		ConfigTOML: source.ConfigTOML, InstallationID: source.InstallationID,
	})
	if err != nil {
		return profilemgr.ProfileStatus{}, err
	}
	defer session.Close()

	status, queryErr := queryStatus(ctx, session)
	refreshed, readErr := session.ReadAuth()
	if readErr == nil && string(refreshed) != string(source.AuthJSON) {
		if err := q.manager.ReconcileStatusCredentials(profile.Key, source.AuthJSON, refreshed); err != nil {
			return profilemgr.ProfileStatus{}, fmt.Errorf("failed to reconcile refreshed credentials: %w", err)
		}
	}
	if queryErr != nil {
		return profilemgr.ProfileStatus{}, queryErr
	}
	return status, nil
}

type appServerAccount struct {
	Account *struct {
		Type     string  `json:"type"`
		PlanType *string `json:"planType"`
	} `json:"account"`
}

type rateWindow struct {
	UsedPercent       *float64 `json:"usedPercent"`
	WindowDurationMin *int64   `json:"windowDurationMins"`
	ResetsAt          *int64   `json:"resetsAt"`
}

type rateLimit struct {
	Primary         *rateWindow      `json:"primary"`
	Secondary       *rateWindow      `json:"secondary"`
	IndividualLimit *individualLimit `json:"individualLimit"`
}

type individualLimit struct {
	RemainingPercent *int   `json:"remainingPercent"`
	ResetsAt         *int64 `json:"resetsAt"`
}

type rateLimitsResponse struct {
	RateLimitsByLimitID map[string]rateLimit `json:"rateLimitsByLimitId"`
	RateLimits          *rateLimit           `json:"rateLimits"`
}

func queryStatus(ctx context.Context, session *codexapp.Session) (profilemgr.ProfileStatus, error) {
	var account appServerAccount
	if err := session.Request(ctx, "account/read", map[string]bool{"refreshToken": false}, &account); err != nil {
		return profilemgr.ProfileStatus{}, classifyError(err)
	}
	// requiresOpenaiAuth is a server capability requirement, not login state;
	// the account and explicit RPC errors are the supported authentication signals.
	if account.Account == nil {
		return profilemgr.ProfileStatus{}, fmt.Errorf("%w: no account returned", ErrSignInRequired)
	}
	if !strings.EqualFold(account.Account.Type, "chatgpt") {
		return profilemgr.ProfileStatus{}, fmt.Errorf("%w: account type is not ChatGPT", ErrSignInRequired)
	}
	var response rateLimitsResponse
	if err := session.Request(ctx, "account/rateLimits/read", map[string]any{}, &response); err != nil {
		return profilemgr.ProfileStatus{}, classifyError(err)
	}
	status, err := statusFromRateLimits(response, time.Now().UTC())
	if err != nil {
		return profilemgr.ProfileStatus{}, err
	}
	status.PlanType = planTypeFromServer(account.Account.PlanType)
	return status, nil
}

func planTypeFromServer(value *string) profilemgr.Plan {
	if value == nil {
		return profilemgr.PlanUnknown
	}
	return profilemgr.Plan(strings.TrimSpace(*value))
}

func statusFromRateLimits(response rateLimitsResponse, fetchedAt time.Time) (profilemgr.ProfileStatus, error) {
	limits, ok := response.RateLimitsByLimitID["codex"]
	if !ok {
		if response.RateLimits == nil {
			return profilemgr.ProfileStatus{}, errors.New("codex did not return a usable quota window")
		}
		limits = *response.RateLimits
	}
	quotas := make([]profilemgr.ProfileQuota, 0, 3)
	for _, window := range []*rateWindow{limits.Primary, limits.Secondary} {
		if quota, ok := quotaFromRateWindow(window); ok {
			quotas = append(quotas, quota)
		}
	}
	if quota, ok := quotaFromIndividualLimit(limits.IndividualLimit); ok {
		quotas = append(quotas, quota)
	}
	if len(quotas) == 0 {
		return profilemgr.ProfileStatus{}, errors.New("codex did not return a usable quota window")
	}
	sort.SliceStable(quotas, func(i, j int) bool {
		if quotas[i].Monthly != quotas[j].Monthly {
			return !quotas[i].Monthly
		}
		return quotas[i].WindowDurationMins < quotas[j].WindowDurationMins
	})

	nearest := quotas[0]
	percent := 100 - nearest.RemainingPercent
	reset := nearest.ResetsAt
	if window, err := shortestWindow(limits); err == nil {
		percent = normalizedPercent(*window.UsedPercent)
		reset = time.Unix(*window.ResetsAt, 0).UTC()
	}
	return profilemgr.ProfileStatus{
		FetchedAt: fetchedAt.UTC(), AuthStatus: profilemgr.ProfileAuthAuthenticated,
		Quotas: quotas, UsedPercent: &percent, ResetsAt: &reset,
	}, nil
}

func quotaFromRateWindow(window *rateWindow) (profilemgr.ProfileQuota, bool) {
	if !usableRateWindow(window) {
		return profilemgr.ProfileQuota{}, false
	}
	usedPercent := normalizedPercent(*window.UsedPercent)
	return profilemgr.ProfileQuota{
		WindowDurationMins: *window.WindowDurationMin,
		RemainingPercent:   100 - usedPercent,
		ResetsAt:           time.Unix(*window.ResetsAt, 0).UTC(),
	}, true
}

func quotaFromIndividualLimit(limit *individualLimit) (profilemgr.ProfileQuota, bool) {
	if limit == nil || limit.RemainingPercent == nil || limit.ResetsAt == nil || *limit.ResetsAt <= 0 {
		return profilemgr.ProfileQuota{}, false
	}
	return profilemgr.ProfileQuota{
		Monthly:          true,
		RemainingPercent: max(0, min(100, *limit.RemainingPercent)),
		ResetsAt:         time.Unix(*limit.ResetsAt, 0).UTC(),
	}, true
}

func shortestWindow(limits rateLimit) (*rateWindow, error) {
	var selected *rateWindow
	for _, window := range []*rateWindow{limits.Primary, limits.Secondary} {
		if !usableRateWindow(window) {
			continue
		}
		if selected == nil || *window.WindowDurationMin < *selected.WindowDurationMin {
			selected = window
		}
	}
	if selected == nil {
		return nil, errors.New("codex did not return a usable quota window")
	}
	return selected, nil
}

func usableRateWindow(window *rateWindow) bool {
	return window != nil && window.UsedPercent != nil && window.WindowDurationMin != nil &&
		window.ResetsAt != nil && *window.WindowDurationMin > 0 && *window.ResetsAt > 0 &&
		!math.IsNaN(*window.UsedPercent) && !math.IsInf(*window.UsedPercent, 0)
}

func normalizedPercent(percent float64) int {
	used := int(math.Round(percent))
	return max(0, min(100, used))
}

func classifyError(err error) error {
	var rpc *codexapp.RPCError
	if !errors.As(err, &rpc) {
		return err
	}
	message := strings.ToLower(rpc.Message)
	for _, phrase := range []string{"not logged in", "login required", "sign-in required", "authentication required", "unauthorized", "no chatgpt account"} {
		if strings.Contains(message, phrase) {
			return ErrSignInRequired
		}
	}
	return fmt.Errorf("codex rejected the status request: %s", strings.TrimSpace(rpc.Message))
}
