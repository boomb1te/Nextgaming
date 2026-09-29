package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
	"nextgaming/internal/wol"
)

// Timing of a wake test. Variables so tests can shorten them.
var (
	wolShutdownTimeout = 120 * time.Second // wait for the PC to go offline
	wolPowerOffWait    = 25 * time.Second  // let it power down fully
	wolBootTimeout     = 240 * time.Second // wait for the agent to reconnect
	wolResendEvery     = 10 * time.Second
)

// wolReport stores what the agent found (and fixed) on the Windows side.
func (s *Server) wolReport(pcID int64, rep protocol.WoLReport, raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pc, err := store.PCByID(s.st.DB, pcID)
	if err != nil {
		return
	}
	w := pc.WoL
	w.Report = string(raw)
	w.CheckedAt = s.now()
	prev := w.Status
	switch {
	case !rep.Supported:
		w.Status, w.Detail = core.WoLUnknown, "This agent cannot check Wake-on-LAN (it is not running on Windows)."
		if rep.Error != "" {
			w.Detail = "The check failed: " + rep.Error
		}
	case !rep.Wired:
		w.Status, w.Detail = core.WoLWiFi, "This PC is not connected by an Ethernet cable. Wake-on-LAN needs a wired connection."
	case !rep.WindowsOK:
		var bad []string
		for _, st := range rep.Settings {
			if !st.OK {
				line := st.Name
				if st.Advice != "" {
					line += ": " + st.Advice
				}
				bad = append(bad, line)
			}
		}
		w.Status, w.Detail = core.WoLWindowsSettings, "Windows settings still need changes: "+strings.Join(bad, "; ")
	case rep.FixedAny:
		var fixed []string
		for _, st := range rep.Settings {
			if st.Fixed {
				fixed = append(fixed, st.Name)
			}
		}
		w.Status, w.Detail = core.WoLFixed, "NextGaming corrected: "+strings.Join(fixed, ", ")+". Run a wake test to check the BIOS side."
	default:
		// Windows side is fine. Keep a known test result; otherwise it is untested.
		if prev != core.WoLOK && prev != core.WoLBIOSNeeded && prev != core.WoLTesting {
			w.Status, w.Detail = core.WoLUntested, "Windows settings are correct. Run a wake test to check the BIOS side."
		}
	}
	if s.wolTestRunning(pcID) {
		w.Status = core.WoLTesting // the running test decides the final status
	}
	if err := store.UpdatePCWoL(s.st.DB, pcID, w); err != nil {
		log.Printf("wol report: %v", err)
	}
	if rep.FixedAny {
		store.Audit(s.st.DB, 0, "wol_autofix", pcID, w.Detail)
	}
	s.changed()
}

// WoLCheck asks the agent to check (and optionally fix) Windows WoL settings.
func (s *Server) WoLCheck(adminID, pcID int64, fix bool) error {
	if err := s.hub.Command(pcID, protocol.TypeWoLCheck, protocol.WoLCheck{Fix: fix}, 90*time.Second); err != nil {
		return errConflict("%v", err)
	}
	store.Audit(s.st.DB, adminID, "wol_check", pcID, fmt.Sprintf("fix=%v", fix))
	return nil
}

func (s *Server) wolTestRunning(pcID int64) bool {
	s.wolMu.Lock()
	defer s.wolMu.Unlock()
	return s.wolTests[pcID]
}

func (s *Server) setWoLStage(pcID int64, status, stage, detail string) {
	pc, err := store.PCByID(s.st.DB, pcID)
	if err != nil {
		return
	}
	pc.WoL.Status, pc.WoL.Stage = status, stage
	if detail != "" {
		pc.WoL.Detail = detail
	}
	if status != core.WoLTesting {
		pc.WoL.Stage = ""
		pc.WoL.TestedAt = s.now()
	}
	store.UpdatePCWoL(s.st.DB, pcID, pc.WoL)
	s.changed()
}

// StartWoLTest shuts a free PC down and tries to wake it with a magic packet.
// It returns immediately; progress is shown in the admin panel.
func (s *Server) StartWoLTest(adminID, pcID int64) error {
	pc, err := store.PCByID(s.st.DB, pcID)
	if err != nil {
		return errNotFound("PC")
	}
	if !s.hub.Online(pcID) {
		return errConflict("%s must be on to run the test", pc.Name)
	}
	if sess, _ := store.OpenSessionForPC(s.st.DB, pcID); sess != nil {
		return errConflict("%s is in use; run the test when it is free", pc.Name)
	}
	if pc.MAC == "" {
		return errConflict("%s has not reported its network address", pc.Name)
	}
	switch pc.WoL.Status {
	case core.WoLWiFi:
		return errConflict("%s is on Wi-Fi; connect an Ethernet cable first", pc.Name)
	case core.WoLWindowsSettings:
		return errConflict("fix the Windows settings first (Check & fix)")
	}
	s.wolMu.Lock()
	if s.wolTests[pcID] {
		s.wolMu.Unlock()
		return errConflict("a test is already running on %s", pc.Name)
	}
	s.wolTests[pcID] = true
	s.wolMu.Unlock()

	store.Audit(s.st.DB, adminID, "wol_test", pcID, "")
	go s.runWoLTest(pc)
	return nil
}

func (s *Server) runWoLTest(pc core.PC) {
	defer func() {
		s.wolMu.Lock()
		delete(s.wolTests, pc.ID)
		s.wolMu.Unlock()
		s.changed()
	}()
	fail := func(status, detail string) {
		s.setWoLStage(pc.ID, status, "", detail)
		level := "warn"
		if status == core.WoLBIOSNeeded {
			level = "error"
		}
		s.alert(level, pc.ID, "Wake test on %s failed: %s", pc.Name, detail)
	}

	s.setWoLStage(pc.ID, core.WoLTesting, "Shutting the PC down…", "")
	if err := s.hub.Command(pc.ID, protocol.TypeShutdown, nil, 15*time.Second); err != nil {
		fail(core.WoLUntested, "could not shut the PC down ("+err.Error()+")")
		return
	}
	if !waitFor(wolShutdownTimeout, func() bool { return !s.hub.Online(pc.ID) }) {
		fail(core.WoLUntested, "the PC did not shut down, so the test could not run")
		return
	}
	s.setWoLStage(pc.ID, core.WoLTesting, "Waiting for the PC to power off completely…", "")
	time.Sleep(wolPowerOffWait)

	s.setWoLStage(pc.ID, core.WoLTesting, "Sending the wake signal…", "")
	deadline := time.Now().Add(wolBootTimeout)
	nextSend := time.Now()
	sends := 0
	for time.Now().Before(deadline) {
		if s.hub.Online(pc.ID) {
			s.setWoLStage(pc.ID, core.WoLOK, "", "Wake-on-LAN works: the PC turned on from the wake signal.")
			s.alert("ok", pc.ID, "Wake test on %s succeeded.", pc.Name)
			return
		}
		if sends < 3 && !time.Now().Before(nextSend) {
			if err := wol.Send(pc.MAC, pc.IP); err != nil {
				fail(core.WoLUntested, "could not send the wake signal: "+err.Error())
				return
			}
			sends++
			nextSend = time.Now().Add(wolResendEvery)
			if sends == 1 {
				s.setWoLStage(pc.ID, core.WoLTesting, "Wake signal sent. Waiting for Windows to start…", "")
			}
		}
		time.Sleep(time.Second)
	}
	fail(core.WoLBIOSNeeded, "the PC did not turn on. Windows settings are correct, so Wake-on-LAN is most likely disabled in the BIOS. Turn the PC on by hand and follow the BIOS steps.")
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return cond()
}
