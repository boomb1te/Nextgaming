package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"nextgaming/internal/core"
)

// ---- clients ----

func scanClient(s interface {
	Scan(...any) error
}) (core.Client, error) {
	var c core.Client
	var created int64
	if err := s.Scan(&c.ID, &c.Name, &c.Phone, &c.Note, &created); err != nil {
		return c, err
	}
	c.CreatedAt = fromTS(created)
	return c, nil
}

// CreateClient inserts a client and returns its id.
func CreateClient(q Q, name, phone, note string) (int64, error) {
	res, err := q.Exec("INSERT INTO clients(name,phone,note,created_at) VALUES(?,?,?,?)",
		strings.TrimSpace(name), strings.TrimSpace(phone), strings.TrimSpace(note), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateClient changes a client's fields.
func UpdateClient(q Q, id int64, name, phone, note string) error {
	_, err := q.Exec("UPDATE clients SET name=?,phone=?,note=? WHERE id=? AND deleted_at=0",
		strings.TrimSpace(name), strings.TrimSpace(phone), strings.TrimSpace(note), id)
	return err
}

// DeleteClient soft-deletes a client and drops its group memberships.
func DeleteClient(q Q, id int64) error {
	if _, err := q.Exec("UPDATE clients SET deleted_at=? WHERE id=?", time.Now().Unix(), id); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM group_members WHERE client_id=?", id)
	return err
}

// ClientByID returns one client.
func ClientByID(q Q, id int64) (core.Client, error) {
	row := q.QueryRow("SELECT id,name,phone,note,created_at FROM clients WHERE id=? AND deleted_at=0", id)
	c, err := scanClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// ListClients returns clients, newest first, optionally filtered by a name or
// phone substring.
func ListClients(q Q, search string) ([]core.Client, error) {
	search = strings.TrimSpace(search)
	var rows *sql.Rows
	var err error
	if search == "" {
		rows, err = q.Query("SELECT id,name,phone,note,created_at FROM clients WHERE deleted_at=0 ORDER BY id DESC")
	} else {
		like := "%" + search + "%"
		rows, err = q.Query("SELECT id,name,phone,note,created_at FROM clients WHERE deleted_at=0 AND (name LIKE ? OR phone LIKE ?) ORDER BY id DESC", like, like)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Client{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- groups ----

// CreateGroup inserts a group (members are added separately).
func CreateGroup(q Q, name, note string) (int64, error) {
	res, err := q.Exec("INSERT INTO client_groups(name,note,created_at) VALUES(?,?,?)",
		strings.TrimSpace(name), strings.TrimSpace(note), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateGroup changes a group's name/note.
func UpdateGroup(q Q, id int64, name, note string) error {
	_, err := q.Exec("UPDATE client_groups SET name=?,note=? WHERE id=? AND deleted_at=0",
		strings.TrimSpace(name), strings.TrimSpace(note), id)
	return err
}

// DeleteGroup soft-deletes a group and removes its memberships.
func DeleteGroup(q Q, id int64) error {
	if _, err := q.Exec("UPDATE client_groups SET deleted_at=? WHERE id=?", time.Now().Unix(), id); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM group_members WHERE group_id=?", id)
	return err
}

// AddGroupMember links a client to a group (idempotent).
func AddGroupMember(q Q, groupID, clientID int64) error {
	_, err := q.Exec("INSERT OR IGNORE INTO group_members(group_id,client_id) VALUES(?,?)", groupID, clientID)
	return err
}

// RemoveGroupMember unlinks a client from a group.
func RemoveGroupMember(q Q, groupID, clientID int64) error {
	_, err := q.Exec("DELETE FROM group_members WHERE group_id=? AND client_id=?", groupID, clientID)
	return err
}

// GroupMemberCount counts a group's (non-deleted) members.
func GroupMemberCount(q Q, groupID int64) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM group_members m
		JOIN clients c ON c.id=m.client_id AND c.deleted_at=0
		WHERE m.group_id=?`, groupID).Scan(&n)
	return n, err
}

// GroupMembers returns a group's members (non-deleted clients).
func GroupMembers(q Q, groupID int64) ([]core.Client, error) {
	rows, err := q.Query(`SELECT c.id,c.name,c.phone,c.note,c.created_at FROM group_members m
		JOIN clients c ON c.id=m.client_id AND c.deleted_at=0
		WHERE m.group_id=? ORDER BY c.name COLLATE NOCASE`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Client{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GroupByID returns a group with its members.
func GroupByID(q Q, id int64) (core.ClientGroup, error) {
	var g core.ClientGroup
	var created int64
	err := q.QueryRow("SELECT id,name,note,created_at FROM client_groups WHERE id=? AND deleted_at=0", id).
		Scan(&g.ID, &g.Name, &g.Note, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	g.CreatedAt = fromTS(created)
	g.Members, err = GroupMembers(q, id)
	return g, err
}

// ListGroups returns all groups with their members.
func ListGroups(q Q) ([]core.ClientGroup, error) {
	rows, err := q.Query("SELECT id,name,note,created_at FROM client_groups WHERE deleted_at=0 ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	out := []core.ClientGroup{}
	for rows.Next() {
		var g core.ClientGroup
		var created int64
		if err := rows.Scan(&g.ID, &g.Name, &g.Note, &created); err != nil {
			return nil, err
		}
		g.CreatedAt = fromTS(created)
		out = append(out, g)
		ids = append(ids, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Members, err = GroupMembers(q, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}
