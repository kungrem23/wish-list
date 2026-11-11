package httpapi

import (
	"net/http"

	"wishlist/internal/accounts"
)

func (a api) sessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode,
	}
}

func (a api) setSession(w http.ResponseWriter, session accounts.Session) error {
	cookie := a.sessionCookie(session.Token)
	cookie.Expires = session.ExpiresAt
	http.SetCookie(w, cookie)
	return noContent(w, nil)
}

func (a api) register(w http.ResponseWriter, r *http.Request) error {
	input, err := readJSON[accounts.Registration](w, r)
	if err != nil {
		return err
	}
	if err := a.authLimits.acquire(w, r, input.Email); err != nil {
		return err
	}
	defer a.authLimits.release()
	session, err := a.accounts.Register(r.Context(), input)
	if err != nil {
		return err
	}
	return a.setSession(w, session)
}

func (a api) login(w http.ResponseWriter, r *http.Request) error {
	input, err := readJSON[accounts.Credentials](w, r)
	if err != nil {
		return err
	}
	if err := a.authLimits.acquire(w, r, input.Email); err != nil {
		return err
	}
	defer a.authLimits.release()
	session, err := a.accounts.Login(r.Context(), input)
	if err != nil {
		return err
	}
	return a.setSession(w, session)
}

func (a api) logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(cookieName); err == nil {
		if err := a.accounts.Logout(r.Context(), cookie.Value); err != nil {
			return err
		}
	}
	cookie := a.sessionCookie("")
	cookie.MaxAge = -1
	http.SetCookie(w, cookie)
	return noContent(w, nil)
}

func (a api) profile(w http.ResponseWriter, r *http.Request, userID int64) error {
	profile, err := a.accounts.Profile(r.Context(), userID)
	if err != nil {
		return err
	}
	return a.writeProfile(w, profile)
}

func (a api) updateProfile(w http.ResponseWriter, r *http.Request, userID int64) error {
	input, err := readJSON[accounts.ProfileInput](w, r)
	if err != nil {
		return err
	}
	profile, err := a.accounts.UpdateProfile(r.Context(), userID, input)
	if err != nil {
		return err
	}
	return a.writeProfile(w, profile)
}

func (a api) writeProfile(w http.ResponseWriter, profile accounts.Profile) error {
	return writeJSON(w, http.StatusOK, struct {
		accounts.Profile
		AvatarUploadsEnabled bool `json:"avatar_uploads_enabled"`
	}{profile, a.photos != nil})
}
