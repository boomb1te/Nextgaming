package server

import (
	"fmt"
	"strings"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
)

// ---- clients ----

// ListClients returns clients, optionally filtered by a name/phone search.
func (s *Server) ListClients(search string) ([]core.Client, error) {
	return store.ListClients(s.st.DB, search)
}

// CreateClient adds a client. Name and phone are both required (not unique).
func (s *Server) CreateClient(adminID int64, name, phone, note string) (int64, error) {
	name, phone = strings.TrimSpace(name), strings.TrimSpace(phone)
	if name == "" {
		return 0, errBad("name is required")
	}
	if phone == "" {
		return 0, errBad("phone number is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.st.Tx(func(q store.Q) error {
		var err error
		if id, err = store.CreateClient(q, name, phone, note); err != nil {
			return err
		}
		return store.Audit(q, adminID, "client_create", 0, fmt.Sprintf("%s %s", name, phone))
	})
	return id, err
}

// UpdateClient edits a client.
func (s *Server) UpdateClient(adminID, id int64, name, phone, note string) error {
	name, phone = strings.TrimSpace(name), strings.TrimSpace(phone)
	if name == "" {
		return errBad("name is required")
	}
	if phone == "" {
		return errBad("phone number is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.ClientByID(q, id); err != nil {
			return errNotFound("client")
		}
		if err := store.UpdateClient(q, id, name, phone, note); err != nil {
			return err
		}
		return store.Audit(q, adminID, "client_update", 0, fmt.Sprintf("%d %s %s", id, name, phone))
	})
}

// DeleteClient removes a client (soft delete; booking history is kept).
func (s *Server) DeleteClient(adminID, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.ClientByID(q, id); err != nil {
			return errNotFound("client")
		}
		if err := store.DeleteClient(q, id); err != nil {
			return err
		}
		return store.Audit(q, adminID, "client_delete", 0, fmt.Sprint(id))
	})
}

// ---- groups ----

// ListGroups returns all groups with their members.
func (s *Server) ListGroups() ([]core.ClientGroup, error) {
	return store.ListGroups(s.st.DB)
}

// GetGroup returns one group with its members.
func (s *Server) GetGroup(id int64) (core.ClientGroup, error) {
	g, err := store.GroupByID(s.st.DB, id)
	if err == store.ErrNotFound {
		return g, errNotFound("group")
	}
	return g, err
}

// CreateGroup adds a group. A group needs at least one member.
func (s *Server) CreateGroup(adminID int64, name, note string, memberIDs []int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errBad("group name is required")
	}
	if len(dedupeIDs(memberIDs)) == 0 {
		return 0, errBad("a group needs at least one client")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.st.Tx(func(q store.Q) error {
		var err error
		if id, err = store.CreateGroup(q, name, note); err != nil {
			return err
		}
		for _, cid := range dedupeIDs(memberIDs) {
			if _, err := store.ClientByID(q, cid); err != nil {
				return errBad("one of the chosen clients does not exist")
			}
			if err := store.AddGroupMember(q, id, cid); err != nil {
				return err
			}
		}
		return store.Audit(q, adminID, "group_create", 0, name)
	})
	return id, err
}

// UpdateGroup edits a group's name/note.
func (s *Server) UpdateGroup(adminID, id int64, name, note string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errBad("group name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.GroupByID(q, id); err != nil {
			return errNotFound("group")
		}
		if err := store.UpdateGroup(q, id, name, note); err != nil {
			return err
		}
		return store.Audit(q, adminID, "group_update", 0, fmt.Sprintf("%d %s", id, name))
	})
}

// DeleteGroup removes a group (its members stay as clients).
func (s *Server) DeleteGroup(adminID, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.GroupByID(q, id); err != nil {
			return errNotFound("group")
		}
		if err := store.DeleteGroup(q, id); err != nil {
			return err
		}
		return store.Audit(q, adminID, "group_delete", 0, fmt.Sprint(id))
	})
}

// AddGroupMember adds a client to a group.
func (s *Server) AddGroupMember(adminID, groupID, clientID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.GroupByID(q, groupID); err != nil {
			return errNotFound("group")
		}
		if _, err := store.ClientByID(q, clientID); err != nil {
			return errNotFound("client")
		}
		if err := store.AddGroupMember(q, groupID, clientID); err != nil {
			return err
		}
		return store.Audit(q, adminID, "group_member_add", 0, fmt.Sprintf("group %d + client %d", groupID, clientID))
	})
}

// RemoveGroupMember removes a client from a group, keeping at least one member.
func (s *Server) RemoveGroupMember(adminID, groupID, clientID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.GroupByID(q, groupID); err != nil {
			return errNotFound("group")
		}
		n, err := store.GroupMemberCount(q, groupID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return errConflict("a group must keep at least one client; add another before removing this one")
		}
		if err := store.RemoveGroupMember(q, groupID, clientID); err != nil {
			return err
		}
		return store.Audit(q, adminID, "group_member_remove", 0, fmt.Sprintf("group %d - client %d", groupID, clientID))
	})
}

func dedupeIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
