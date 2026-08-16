package profilestatus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
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
	RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
	Account            *struct {
		Type string `json:"type"`
	} `json:"account"`
}

type rateWindow struct {
	UsedPercent       *float64 `json:"usedPercent"`
	WindowDurationMin *int64   `json:"windowDurationMins"`
	ResetsAt          *int64   `json:"resetsAt"`
}

type rateLimit struct {
	Primary   *rateWindow `json:"primary"`
	Secondary *rateWindow `json:"secondary"`
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
	if account.RequiresOpenAIAuth || account.Account == nil || !strings.EqualFold(account.Account.Type, "chatgpt") {
		return profilemgr.ProfileStatus{}, ErrSignInRequired
	}
	var response rateLimitsResponse
	if err := session.Request(ctx, "account/rateLimits/read", map[string]any{}, &response); err != nil {
		return profilemgr.ProfileStatus{}, classifyError(err)
	}
	return statusFromRateLimits(response, time.Now().UTC())
}

func statusFromRateLimits(response rateLimitsResponse, fetchedAt time.Time) (profilemgr.ProfileStatus, error) {
	limits, ok := response.RateLimitsByLimitID["codex"]
	if !ok {
		if response.RateLimits == nil {
			return profilemgr.ProfileStatus{}, errors.New("codex did not return a usable quota window")
		}
		limits = *response.RateLimits
	}
	window, err := shortestWindow(limits)
	if err != nil {
		return profilemgr.ProfileStatus{}, err
	}
	percent := int(math.Round(*window.UsedPercent))
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	reset := time.Unix(*window.ResetsAt, 0).UTC()
	return profilemgr.ProfileStatus{
		FetchedAt: fetchedAt.UTC(), AuthStatus: profilemgr.ProfileAuthAuthenticated,
		UsedPercent: &percent, ResetsAt: &reset,
	}, nil
}

func shortestWindow(limits rateLimit) (*rateWindow, error) {
	var selected *rateWindow
	for _, window := range []*rateWindow{limits.Primary, limits.Secondary} {
		if window == nil || window.UsedPercent == nil || window.WindowDurationMin == nil ||
			window.ResetsAt == nil || *window.WindowDurationMin <= 0 || *window.ResetsAt <= 0 ||
			math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) {
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
