package ops

import "github.com/coder/websocket"

// SetPublicURL changes the origin agents are given (tests learn their URL after boot).
func (s *Service) SetPublicURL(u string) { s.cfg.PublicURL = u }

// Disconnect drops a server's live agent connection (the server was deleted, or re-enrolled with
// a new key). The agent's next challenge then fails against whatever the row now says.
func (s *Service) Disconnect(serverID string) {
	if c := s.hub.get(serverID); c != nil {
		s.hub.remove(c)
		_ = c.ws.Close(websocket.StatusPolicyViolation, "disconnected by the control plane")
	}
	s.broker.Publish(ServerTopic(serverID))
}
