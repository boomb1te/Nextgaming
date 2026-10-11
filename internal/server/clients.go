package server

import (
	"fmt"
	"log"
	"strings"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
)

// ---- clients ----

// ListClients returns clients, optionally filtered by a name/phone search.
func (s *Server) ListClients(search string) ([]core.Client, error) {
	return store.ListClients(s.st.DB, search)
}

// CreateClient adds a client. Name is required; phone must be a valid Turkmen
// number and is now unique (names may still repeat). The phone is stored in
// canonical +993 form.
func (s *Server) CreateClient(adminID int64, name, phone, note string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errBad("name is required")
	}
	canonical, key, ok := core.NormalizeTMPhone(phone)
	if !ok {
		return 0, errBad("enter a valid Turkmen phone number (8 digits, e.g. 65 12 34 56)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.st.Tx(func(q store.Q) error {
		if ex, found, err := store.ClientByPhoneKey(q, key); err != nil {
			return err
		} else if found {
			return errConflict("that number already belongs to %s", ex.Name)
		}
		var err error
		if id, err = store.CreateClient(q, name, canonical, note); err != nil {
			return err
		}
		return store.Audit(q, adminID, "client_create", 0, fmt.Sprintf("%s %s", name, canonical))
	})
	return id, err
}

// UpdateClient edits a client. The phone stays unique and is re-canonicalised.
func (s *Server) UpdateClient(adminID, id int64, name, phone, note string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errBad("name is required")
	}
	canonical, key, ok := core.NormalizeTMPhone(phone)
	if !ok {
		return errBad("enter a valid Turkmen phone number (8 digits, e.g. 65 12 34 56)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		if _, err := store.ClientByID(q, id); err != nil {
			return errNotFound("client")
		}
		if ex, found, err := store.ClientByPhoneKey(q, key); err != nil {
			return err
		} else if found && ex.ID != id {
			return errConflict("that number already belongs to %s", ex.Name)
		}
		if err := store.UpdateClient(q, id, name, canonical, note); err != nil {
			return err
		}
		return store.Audit(q, adminID, "client_update", 0, fmt.Sprintf("%d %s %s", id, name, canonical))
	})
}

// MatchClientByPhone finds the client an incoming call number belongs to.
// found=false when the number is unusable or no client matches.
func (s *Server) MatchClientByPhone(number string) (core.Client, bool, error) {
	return store.FindClientByPhone(s.st.DB, number)
}

// DuplicateClientPhones lists numbers shared by more than one client (left over
// from before phone numbers were unique); these make call-matching ambiguous.
func (s *Server) DuplicateClientPhones() ([]store.PhoneDup, error) {
	return store.DuplicatePhoneKeys(s.st.DB)
}

// warnDuplicatePhones logs any pre-existing duplicate client numbers at startup
// so the owner can resolve them (call-matching picks one deterministically).
func (s *Server) warnDuplicatePhones() {
	dups, err := store.DuplicatePhoneKeys(s.st.DB)
	if err != nil || len(dups) == 0 {
		return
	}
	log.Printf("clients: %d phone number(s) are shared by more than one client; call-matching will be ambiguous until you merge them:", len(dups))
	for _, d := range dups {
		names := make([]string, 0, len(d.Clients))
		for _, c := range d.Clients {
			names = append(names, c.Name)
		}
		log.Printf("  +993%s: %s", d.Key, strings.Join(names, ", "))
	}
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
