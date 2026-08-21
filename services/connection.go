package services

import (
	"context"
	"fmt"

	"github.com/MuhibAhmed/openvpn-desktop/internal/creds"
	"github.com/MuhibAhmed/openvpn-desktop/internal/settings"
	"github.com/MuhibAhmed/openvpn-desktop/internal/vpn"
)

// ConnectionService drives the tunnel.
type ConnectionService struct {
	deps *Deps
}

// NewConnectionService returns the service.
func NewConnectionService(deps *Deps) *ConnectionService {
	return &ConnectionService{deps: deps}
}

// Connect brings up a profile, replacing any current connection.
func (s *ConnectionService) Connect(profileID string) error {
	p, err := s.deps.Profiles.Get(profileID)
	if err != nil {
		return err
	}

	if _, err := s.deps.Settings.Update(func(cur *settings.Settings) {
		cur.LastProfileID = profileID
	}); err != nil {
		// Failing to remember the choice is not a reason to refuse to connect.
		_ = err
	}

	return s.deps.Manager.Connect(context.Background(), p)
}

// Disconnect brings down the current connection.
func (s *ConnectionService) Disconnect() error {
	return s.deps.Manager.Disconnect()
}

// Status returns the current connection state. The UI also receives this as an
// event on every change; this method is for the initial render and for
// recovering after a reload.
func (s *ConnectionService) Status() vpn.Status {
	return s.deps.Manager.Status()
}

// Logs returns the current session's log lines.
func (s *ConnectionService) Logs() []vpn.LogLine {
	return s.deps.Manager.Logs()
}

// SubmitCredentials answers an outstanding sign-in prompt.
//
// Saving is done here rather than in the UI so the secret crosses the boundary
// once and is never handed back.
func (s *ConnectionService) SubmitCredentials(answer vpn.Answer) error {
	status := s.deps.Manager.Status()
	if status.Prompt == nil {
		return fmt.Errorf("nothing is waiting for credentials")
	}
	kind := status.Prompt.Kind

	if err := s.deps.Manager.Answer(answer); err != nil {
		return err
	}

	if !answer.Remember || status.ProfileID == "" {
		return nil
	}

	// What gets saved depends on what was asked. The account credentials and the
	// private key passphrase are different secrets and live in different vault
	// entries; a one-time code is never saved, because storing it would defeat
	// the point of having it.
	slot, save := creds.SlotAuth, creds.Credentials{
		Username: answer.Username,
		Password: answer.Password,
	}
	switch kind {
	case vpn.PromptPassphrase:
		slot = creds.SlotPrivateKey
		save = creds.Credentials{Password: answer.Password}
	case vpn.PromptStaticChallenge, vpn.PromptDynamicChallenge:
		// The password half is reusable, the second factor is not.
		save.Password = answer.Password
	}

	// A blank password is legitimate: openvpn supports servers that only want a
	// username, so saving the username alone is still worth doing.
	if save.Empty() {
		return nil
	}
	return s.deps.Creds.Save(status.ProfileID, slot, save)
}

// AutoFill returns saved credentials for the profile currently prompting, so
// the dialog can offer them. It returns the password because the user has
// already chosen to save it and the dialog needs to submit it; nothing is
// persisted to the frontend beyond the life of the dialog.
type AutoFill struct {
	Found    bool   `json:"found"`
	Username string `json:"username"`
	Password string `json:"password"`
	// FromLegacyGUI marks values recovered from the OpenVPN community GUI
	// rather than from our own vault. The dialog says so, because filling in a
	// secret the user did not give this app should never be silent.
	FromLegacyGUI bool `json:"fromLegacyGui"`
}

// SavedCredentialsForCurrent returns whatever is stored for the secret the
// current prompt is asking about.
func (s *ConnectionService) SavedCredentialsForCurrent() AutoFill {
	status := s.deps.Manager.Status()
	if status.ProfileID == "" || status.Prompt == nil {
		return AutoFill{}
	}

	// A prompt only reaches the UI when there was nothing stored, or when what
	// was stored has just been rejected. Offering the rejected value back would
	// invite the user to submit the same wrong secret again, so retries start
	// empty.
	if status.Prompt.Retry {
		return AutoFill{}
	}

	stored, ok := s.deps.StoredSecretFor(status.ProfileID, status.Prompt.Kind)
	if !ok {
		return AutoFill{}
	}
	return AutoFill{
		Found:         true,
		Username:      stored.Username,
		Password:      stored.Password,
		FromLegacyGUI: stored.FromLegacyGUI,
	}
}
