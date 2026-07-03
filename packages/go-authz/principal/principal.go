package principal

type Type string

const (
	TypeAnonymous Type = "anonymous"
	TypeUser      Type = "user"
	TypeService   Type = "service"
)

type Principal struct {
	Type        Type
	UserID      string
	Roles       []string
	AuthMethods []string
	AuthLevel   string
	SessionID   string
	ClientType  string
	DeviceID    string
}

func Anonymous() Principal {
	return Principal{Type: TypeAnonymous}
}
