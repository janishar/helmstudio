package supervisor

// ErrorKind classifies a refusal so the API can choose a status code and the
// UI a sentence, without parsing a message.
type ErrorKind string

const (
	KindNotFound       ErrorKind = "not_found"
	KindAlreadyRunning ErrorKind = "already_running"
	KindNotRunning     ErrorKind = "not_running"
	KindNotLaunchable  ErrorKind = "not_launchable"
	KindPortConflict   ErrorKind = "port_conflict"
	KindHeavyConflict  ErrorKind = "heavy_conflict"
)

// Error is a refusal a user can act on. Message is written for them.
type Error struct {
	Kind    ErrorKind
	Message string
	// Heavy carries the numbers behind a KindHeavyConflict refusal.
	Heavy *HeavyArithmetic
}

func (e *Error) Error() string { return e.Message }

// HeavyArithmetic is the memory statement made before the one-heavy-group
// rule is enforced (docs/design/01-prd.md R25). A zero GB value means the
// manifest does not declare peak_ram_gb; a zero HostGB means the host's
// memory could not be read.
type HeavyArithmetic struct {
	RunningStudio string `json:"running_studio"`
	RunningName   string `json:"running_name"`
	RunningPeakGB int    `json:"running_peak_gb"`
	WantedStudio  string `json:"wanted_studio"`
	WantedName    string `json:"wanted_name"`
	WantedPeakGB  int    `json:"wanted_peak_gb"`
	HostGB        int    `json:"host_gb"`
}
