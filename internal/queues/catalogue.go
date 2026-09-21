package queues

import (
	"errors"
	"strings"
)

/*
The catalogue: every command this package can run inside a site's web container. Each is a fixed
argv (a function returning a fresh copy of a literal), never built from a caller's text. The agent
hands it to `docker exec` element by element, so no shell reads it — not the host's, not the
container's. It runs in the image's working directory (the app's root), as the image's user.
*/

// Timeouts of the commands, in seconds (the agent kills the command after it).
const (
	readTimeout   = 30
	actionTimeout = 120
)

// Command is one catalogue entry, ready to run.
type Command struct {
	Name    string // what it is, for messages: "queue:failed"
	Argv    []string
	Timeout int
}

// sizesMarker starts the one line of laravelSizesScript's output that is read.
const sizesMarker = "HOSBAH_QUEUE_SIZES="

// laravelSizesScript boots the app the way artisan does and prints Queue::size() of the queues its
// own configuration names: the default connection's queue, Horizon's supervisors (defaults and the
// current environment) and QUEUE_NAMES (comma separated). A queue whose size cannot be read is null.
// It is a constant: nothing is interpolated into it.
const laravelSizesScript = `require 'vendor/autoload.php';
$app = require 'bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
$names = [];
$d = config('queue.connections.'.config('queue.default').'.queue');
if (is_string($d)) { $names[] = $d; }
$sup = array_merge((array) config('horizon.defaults', []), (array) config('horizon.environments.'.config('app.env'), []));
foreach ($sup as $s) { foreach ((array) (is_array($s) ? ($s['queue'] ?? []) : []) as $n) { $names[] = $n; } }
foreach (explode(',', (string) getenv('QUEUE_NAMES')) as $n) { $names[] = trim($n); }
$out = [];
foreach (array_unique(array_filter($names, 'is_string')) as $n) {
    if (!preg_match('/^[A-Za-z0-9_.:\-]{1,120}$/', $n) || $n[0] === '-') { continue; }
    try { $out[$n] = (int) Illuminate\Support\Facades\Queue::size($n); } catch (\Throwable $e) { $out[$n] = null; }
}
echo "\n` + sizesMarker + `".json_encode((object) $out)."\n";`

func laravelFailedJSON() Command {
	return Command{Name: "queue:failed --json", Timeout: readTimeout,
		Argv: []string{"php", "artisan", "queue:failed", "--json", "--no-ansi", "--no-interaction"}}
}

func laravelFailedTable() Command {
	return Command{Name: "queue:failed", Timeout: readTimeout,
		Argv: []string{"php", "artisan", "queue:failed", "--no-ansi", "--no-interaction"}}
}

func laravelSizes() Command {
	return Command{Name: "queue sizes", Timeout: readTimeout, Argv: []string{"php", "-r", laravelSizesScript}}
}

func laravelHorizonStatus() Command {
	return Command{Name: "horizon:status", Timeout: readTimeout,
		Argv: []string{"php", "artisan", "horizon:status", "--no-ansi", "--no-interaction"}}
}

func celeryInspect(app, what string) Command {
	return Command{Name: "celery inspect " + what, Timeout: readTimeout,
		Argv: []string{"celery", "-A", app, "inspect", what, "--json", "--timeout", "5"}}
}

// Actions on failed jobs.
const (
	ActionRetry    = "retry"
	ActionForget   = "forget"
	ActionRetryAll = "retry_all"
	ActionFlush    = "flush"
)

// AuditOf is the audit action of each.
var AuditOf = map[string]string{
	ActionRetry: "queue.retry", ActionForget: "queue.forget", ActionRetryAll: "queue.retry_all", ActionFlush: "queue.flush",
}

// ErrBadJobID: not a failed job's id.
var ErrBadJobID = errors.New("a failed job's id is 1 to 120 characters of A-Z a-z 0-9 _ . : - and does not start with a dash")

// CheckJobID is the rule of a failed job's id as a caller gives it. "all" is refused: to artisan it
// means every failed job, and that is a different, separately audited action.
func CheckJobID(id string) (string, error) {
	if !ValidArg(id) || strings.EqualFold(id, "all") {
		return "", ErrBadJobID
	}
	return id, nil
}

// LaravelAction is the argv of an action on Laravel's failed jobs. jobID is only read by retry and
// forget, where it must pass CheckJobID; it is always an argv element of its own.
func LaravelAction(action, jobID string) (Command, error) {
	argv := func(sub string, rest ...string) []string {
		return append([]string{"php", "artisan", sub, "--no-ansi", "--no-interaction"}, rest...)
	}
	switch action {
	case ActionRetry, ActionForget:
		id, err := CheckJobID(jobID)
		if err != nil {
			return Command{}, err
		}
		sub := "queue:retry"
		if action == ActionForget {
			sub = "queue:forget"
		}
		return Command{Name: sub, Timeout: actionTimeout, Argv: argv(sub, id)}, nil
	case ActionRetryAll:
		return Command{Name: "queue:retry all", Timeout: actionTimeout, Argv: argv("queue:retry", "all")}, nil
	case ActionFlush:
		return Command{Name: "queue:flush", Timeout: actionTimeout, Argv: argv("queue:flush")}, nil
	}
	return Command{}, errors.New("unknown action")
}
