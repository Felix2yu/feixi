package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// notificationStatus accepts the delivery report from a third party (apprise's
// `apprise://` forwarding ends up here too). It always answers 200: rejecting a
// report makes the other party retry forever, and the row may legitimately be
// outside the one-hour window the store accepts.
func (h *apiHandler) notificationStatus(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	errText := r.Form.Get("error")
	if errText == "" {
		// Twilio reports arrive under their own field names.
		switch r.Form.Get("MessageStatus") {
		case "failed", "undelivered", "canceled":
			errText = r.Form.Get("MessageStatus")
		}
	}
	if errText == "" {
		switch r.Form.Get("CallStatus") {
		case "failed", "busy", "no-answer", "canceled", "not-received":
			errText = r.Form.Get("CallStatus")
		}
	}

	markDisabled := r.Form.Get("mark_disabled")
	switch markDisabled {
	case "1", "true", "yes", "on":
		markDisabled = "1"
	default:
		markDisabled = ""
	}

	if _, _, err := h.db.SetNotificationError(r.Context(), chi.URLParam(r, "code"),
		errText, markDisabled == "1"); err != nil {
		writeText(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeText(w, http.StatusOK, "OK")
}

// mountNotifications registers the report endpoint on an already versioned router.
func (h *apiHandler) mountNotifications(r chi.Router) {
	r.Post("/notifications/{code}/status", h.notificationStatus)
}
