//go:build darwin

package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type funnelReceipt struct {
	PID            int   `json:"pid"`
	StartedSeconds int64 `json:"started_seconds"`
	StartedMicros  int32 `json:"started_micros"`
}

func funnelProcess(pid int) (*funnelReceipt, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	// Darwin can report an empty sysctl result (EIO) for an exited process.
	if err != nil && errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
		return nil, nil
	}
	if errors.Is(err, unix.ESRCH) || (err == nil && (info.Proc.P_pid != int32(pid) || info.Eproc.Pgid != int32(pid))) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &funnelReceipt{PID: pid, StartedSeconds: info.Proc.P_starttime.Sec, StartedMicros: info.Proc.P_starttime.Usec}, nil
}

func rememberFunnel(dir string, pid int) error {
	if dir == "" {
		return nil
	}
	record, err := funnelProcess(pid)
	if err != nil {
		return err
	}
	if record == nil {
		return errors.New("tunnel process exited before ownership could be recorded")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".funnel-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "funnel.json"))
}

func forgetFunnel(dir string, pid int) {
	if dir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "funnel.json"))
	var record funnelReceipt
	if err == nil && json.Unmarshal(data, &record) == nil && record.PID == pid {
		_ = os.Remove(filepath.Join(dir, "funnel.json"))
	}
}

// The caller owns the service-state lock. Recover only its recorded tunnel
// group, checking the kernel birth time so a reused PID cannot be signalled.
// macOS launchd cannot reap the separate process group after a service crash.
func recoverFunnel(dir string) error {
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, "funnel.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var record funnelReceipt
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("invalid tunnel ownership record: %w", err)
	}
	if record.PID <= 1 || record.StartedSeconds == 0 {
		return errors.New("invalid tunnel ownership record")
	}
	current, err := funnelProcess(record.PID)
	if err != nil {
		return err
	}
	if current != nil && *current == record {
		if err := unix.Kill(-record.PID, unix.SIGINT); err != nil && !errors.Is(err, unix.ESRCH) {
			return err
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && unix.Kill(-record.PID, 0) == nil {
			time.Sleep(25 * time.Millisecond)
		}
		if unix.Kill(-record.PID, 0) == nil {
			if err := unix.Kill(-record.PID, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
