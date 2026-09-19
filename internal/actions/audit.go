package actions

import (
	"fmt"
	"log/syslog"
	"os"
	"strconv"
	"sync"
)

// Audit records every privileged thing whytop does to the system journal.
//
// whytop runs as root and can kill processes, restart units, edit unit files
// and empty a running process's files. An action that leaves no trace of who
// did it is the gap between "we had an incident" and "we know what happened
// during it" — CIS asks for exactly this accountability on privileged
// operations, and it's cheap here because the actions are few and explicit.
//
// It logs to AUTHPRIV, where privileged-action records belong and where the
// default rules keep them out of world-readable logs. Read-only browsing is
// never logged: the point is the changes.
var (
	auditOnce sync.Once
	auditLog  *syslog.Writer
)

func audit(format string, args ...any) {
	auditOnce.Do(func() {
		// Best effort. A box with no syslog socket (a container, usually)
		// must still be able to run the tool — losing the audit trail is bad,
		// refusing to work at all is worse.
		auditLog, _ = syslog.New(syslog.LOG_NOTICE|syslog.LOG_AUTHPRIV, "whytop")
	})
	if auditLog == nil {
		return
	}
	_ = auditLog.Notice(fmt.Sprintf("%s: %s", actor(), fmt.Sprintf(format, args...)))
}

// actor names the human behind the action. Under sudo the process is root,
// so root is not the useful answer — SUDO_USER is the account that will be
// asked about it afterwards.
func actor() string {
	uid := strconv.Itoa(os.Getuid())
	if who := os.Getenv("SUDO_USER"); who != "" {
		return fmt.Sprintf("%s (via sudo, uid %s)", who, uid)
	}
	return "uid " + uid
}
