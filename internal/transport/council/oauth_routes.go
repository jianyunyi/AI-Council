package council

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/rest/pathvar"
)

type oauthCookieState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
}

func (a *RBACAPI) oauthProviders(w http.ResponseWriter, _ *http.Request) {
	providers := make([]string, 0, 2)
	if a.oauth != nil {
		for _, provider := range []string{"github", "google"} {
			if a.oauth.Enabled(provider) {
				providers = append(providers, provider)
			}
		}
	}
	writeData(w, http.StatusOK, map[string][]string{"providers": providers})
}

func (a *RBACAPI) oauthStart(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(pathvar.Vars(r)["provider"])
	if a.oauth == nil {
		writeErr(w, http.StatusServiceUnavailable, "oauth_unavailable", "OAuth provider is not configured")
		return
	}
	flow, err := a.oauth.Start(r.Context(), provider)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "oauth_unavailable", "OAuth provider is not configured")
		return
	}
	state, err := json.Marshal(oauthCookieState{State: flow.State, Verifier: flow.Verifier, Nonce: flow.Nonce})
	if err != nil {
		writeInternal(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName(provider), Value: base64.RawURLEncoding.EncodeToString(state), Path: "/api/v1/auth/oauth/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600, Expires: time.Now().Add(10 * time.Minute)})
	http.Redirect(w, r, flow.URL, http.StatusFound)
}

func (a *RBACAPI) oauthCallback(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(pathvar.Vars(r)["provider"])
	cookieName := oauthCookieName(provider)
	cookie, err := r.Cookie(cookieName)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/api/v1/auth/oauth/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	if err != nil || a.oauth == nil || r.URL.Query().Get("error") != "" {
		oauthFailure(w, r)
		return
	}
	var flow oauthCookieState
	encoded, decodeErr := base64.RawURLEncoding.DecodeString(cookie.Value)
	if decodeErr != nil || json.Unmarshal(encoded, &flow) != nil || flow.State == "" || flow.Verifier == "" || flow.Nonce == "" {
		oauthFailure(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" || len(state) != len(flow.State) || subtle.ConstantTimeCompare([]byte(state), []byte(flow.State)) != 1 {
		oauthFailure(w, r)
		return
	}
	result, err := a.oauth.Complete(r.Context(), provider, r.URL.Query().Get("code"), state, flow.Verifier, flow.Nonce, a.options.TTL)
	if err != nil {
		oauthFailure(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.options.CookieName, Value: result.AccessToken, Path: "/", HttpOnly: true, Secure: a.options.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(a.options.TTL.Seconds()), Expires: time.Now().Add(a.options.TTL)})
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func oauthFailure(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login?oauth=failed", http.StatusSeeOther)
}

func oauthCookieName(provider string) string { return "aicouncil_oauth_" + provider }
