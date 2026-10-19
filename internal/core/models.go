// Package core holds NextGaming's domain model and pure business logic.
// Nothing in here touches the network, the database or the operating system,
// so every rule (billing, packages, refunds) can be unit tested in isolation.
package core

import "time"

// Money is an amount in minor currency units (1/100 of a manat).
// Integers avoid floating-point rounding errors in cash totals.
type Money int64

// SessionMode is how a session is billed.
type SessionMode string

const (
	// ModePrepaid: the customer pays up front for a fixed time; the PC locks when it runs out.
	ModePrepaid SessionMode = "prepaid"
	// ModeOpen: the timer counts up and the customer pays at the end.
	ModeOpen SessionMode = "open"
	// ModeFree: staff / maintenance unlock. Never billed, always audited.
	ModeFree SessionMode = "free"
)

// SessionStatus is the lifecycle state of a session.
type SessionStatus string

const (
	StatusActive SessionStatus = "active"
	StatusPaused SessionStatus = "paused"
	StatusEnded  SessionStatus = "ended"
)

// Zone groups PCs that share an hourly rate (e.g. Standard, VIP, Console room).
type Zone struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HourlyRate Money  `json:"hourlyRate"`
}

// Package is a fixed-price time bundle ("3 hours", "Night pack").
type Package struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	ZoneID int64  `json:"zoneId"` // 0 = valid in every zone
	// Minutes of play. Ignored when EndsAtHour is set.
	Minutes int   `json:"minutes"`
	Price   Money `json:"price"`
	// EndsAtHour (0-23) makes the package last until that clock hour,
	// e.g. a night pack bought at 23:10 with EndsAtHour=8 runs until 08:00. -1 = unused.
	EndsAtHour int `json:"endsAtHour"`
	// AvailableFrom/AvailableTo (0-23) restrict when the package can be sold.
	// The window may wrap midnight (22 -> 6). -1 on either side = always available.
	AvailableFrom int  `json:"availableFrom"`
	AvailableTo   int  `json:"availableTo"`
	Active        bool `json:"active"`
}

// WoL status values shown in the admin panel.
const (
	WoLUntested        = "untested"         // Windows settings look fine, not tested yet
	WoLOK              = "ok"               // a real wake test succeeded
	WoLFixed           = "fixed"            // agent corrected Windows settings; test recommended
	WoLWindowsSettings = "windows_settings" // Windows settings wrong and could not be fixed
	WoLBIOSNeeded      = "bios_needed"      // Windows OK but the wake test failed -> BIOS
	WoLWiFi            = "wifi"             // no wired connection; WoL needs Ethernet
	WoLTesting         = "testing"          // a wake test is running right now
	WoLUnknown         = "unknown"          // agent has not reported yet
)

// WoLInfo is the Wake-on-LAN state the server keeps per PC.
type WoLInfo struct {
	Status    string    `json:"status"`
	Detail    string    `json:"detail"`
	Stage     string    `json:"stage,omitempty"` // progress text during a test
	CheckedAt time.Time `json:"checkedAt"`
	TestedAt  time.Time `json:"testedAt"`
	Report    string    `json:"-"` // raw JSON report from the agent
}

// PC is one managed computer.
type PC struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	ZoneID       int64     `json:"zoneId"`
	SortOrder    int       `json:"sortOrder"`
	MachineID    string    `json:"machineId"`
	MAC          string    `json:"mac"`
	IP           string    `json:"ip"`
	Hostname     string    `json:"hostname"`
	BoardMaker   string    `json:"boardMaker"`
	BoardProduct string    `json:"boardProduct"`
	AgentVersion string    `json:"agentVersion"`
	WoL          WoLInfo   `json:"wol"`
	Note         string    `json:"note"` // free staff note shown on the tile/modal
	CreatedAt    time.Time `json:"createdAt"`
}

// Session is one period of use of one PC.
type Session struct {
	ID            int64         `json:"id"`
	PCID          int64         `json:"pcId"`
	Mode          SessionMode   `json:"mode"`
	Status        SessionStatus `json:"status"`
	StartedAt     time.Time     `json:"startedAt"`
	EndedAt       *time.Time    `json:"endedAt,omitempty"`
	PausedAt      *time.Time    `json:"pausedAt,omitempty"`
	PausedSeconds int64         `json:"pausedSeconds"`
	// PaidSeconds is the prepaid time allotment (grows with "add time").
	PaidSeconds int64  `json:"paidSeconds"`
	RatePerHour Money  `json:"ratePerHour"` // rate snapshot taken at start
	TariffName  string `json:"tariffName"`
	// Paid is the net cash taken for this session so far (payments minus refunds).
	Paid       Money  `json:"paid"`
	StartedBy  int64  `json:"startedBy"`
	EndedBy    int64  `json:"endedBy"`
	ShiftID    int64  `json:"shiftId"`
	CustomerID *int64 `json:"customerId,omitempty"` // reserved for customer accounts (not used in v1)
	EndReason  string `json:"endReason,omitempty"`
	Note       string `json:"note,omitempty"`
	// CheckoutRequested is set when the player pressed "End session" on the PC.
	// The session is paused (clock stopped) until the admin settles it at the desk.
	CheckoutRequested bool `json:"checkoutRequested"`
}

// Transaction kinds. Every money movement is a row; rows are never edited.
const (
	TxSessionStart = "session_start" // prepaid payment at start
	TxAddTime      = "add_time"      // prepaid top-up of an active session
	TxOpenClose    = "open_close"    // payment collected when an open session ends
	TxRefund       = "refund"        // negative: money returned to the customer
	TxCorrection   = "correction"    // manual correction, positive or negative, reason required
)

// Transaction is one cash movement.
type Transaction struct {
	ID        int64     `json:"id"`
	SessionID int64     `json:"sessionId"`
	PCID      int64     `json:"pcId"`
	ShiftID   int64     `json:"shiftId"`
	AdminID   int64     `json:"adminId"`
	Kind      string    `json:"kind"`
	Amount    Money     `json:"amount"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Shift is one cashier's working period at the front desk.
type Shift struct {
	ID          int64      `json:"id"`
	AdminID     int64      `json:"adminId"`
	OpenedAt    time.Time  `json:"openedAt"`
	ClosedAt    *time.Time `json:"closedAt,omitempty"`
	OpeningCash Money      `json:"openingCash"`
	ClosingCash Money      `json:"closingCash"` // what the cashier counted
	Expected    Money      `json:"expected"`    // opening cash + net takings
	Note        string     `json:"note,omitempty"`
}

// Admin roles.
const (
	RoleOwner   = "owner"
	RoleCashier = "cashier"
)

// Admin is a staff login for the admin panel.
type Admin struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	Disabled  bool      `json:"disabled"`
}

// AuditEntry records who did what.
type AuditEntry struct {
	ID        int64     `json:"id"`
	AdminID   int64     `json:"adminId"`
	Admin     string    `json:"admin"`
	Action    string    `json:"action"`
	PCID      int64     `json:"pcId"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"createdAt"`
}

// Settings are club-wide options editable in the admin panel.
type Settings struct {
	ClubName string `json:"clubName"`
	Currency string `json:"currency"`
	// GraceSeconds: an open session ended within this many seconds costs nothing
	// (covers "started on the wrong PC" mistakes).
	GraceSeconds int `json:"graceSeconds"`
	// MinChargeMinutes: the minimum billable time for an open session.
	MinChargeMinutes int `json:"minChargeMinutes"`
	// WarnMinutes: the client warns the player at these remaining minutes.
	WarnMinutes []int `json:"warnMinutes"`
	// AutoFixWoL lets agents correct Wake-on-LAN settings in Windows automatically.
	AutoFixWoL bool `json:"autoFixWol"`
	// RequireShift: paid sessions can only start while a shift is open.
	RequireShift bool `json:"requireShift"`
	// Appearance pushed to the gaming PCs. The images themselves are stored as
	// files on the server; these counters bump on each upload so agents know to
	// re-fetch. 0 means "no image set" (use the plain default).
	DesktopWallpaperVer int `json:"desktopWallpaperVer"`
	LockScreenVer       int `json:"lockScreenVer"`
	// NamePosition places the PC name on the lock screen: "center" (default) or
	// "top-left".
	NamePosition string `json:"namePosition"`
}

// DefaultSettings are used on first run.
func DefaultSettings() Settings {
	return Settings{
		ClubName:         "NextGaming Club",
		Currency:         "TMT",
		GraceSeconds:     60,
		MinChargeMinutes: 0,
		WarnMinutes:      []int{5, 1},
		AutoFixWoL:       true,
		RequireShift:     true,
		NamePosition:     "center",
	}
}
