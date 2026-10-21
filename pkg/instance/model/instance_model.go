package instance_model

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrInstanceNotFound indicates that a settings operation found no instance row.
var ErrInstanceNotFound = errors.New("instance not found")

type Instance struct {
	Id               string    `json:"id" gorm:"type:uuid;primaryKey"`
	Name             string    `json:"name"`
	Token            string    `json:"token" gorm:"unique"`
	Webhook          string    `json:"webhook"`
	RabbitmqEnable   string    `json:"rabbitmqEnable"`
	WebSocketEnable  string    `json:"websocketEnable"`
	NatsEnable       string    `json:"natsEnable"`
	Jid              string    `json:"jid" gorm:"column:jid"`
	Qrcode           string    `json:"qrcode" gorm:"type:text"`
	Connected        bool      `json:"connected"`
	Expiration       int64     `json:"expiration"`
	DisconnectReason string    `json:"disconnect_reason"`
	Events           string    `json:"events"`
	OsName           string    `json:"os_name"`
	Proxy            string    `json:"proxy"`
	ClientName       string    `json:"client_name"`
	CreatedAt        time.Time `json:"createdAt" gorm:"autoCreateTime"`

	// Advanced Settings
	AlwaysOnline  bool   `json:"alwaysOnline" gorm:"default:false"`
	RejectCall    bool   `json:"rejectCall" gorm:"default:false"`
	MsgRejectCall string `json:"msgRejectCall" gorm:"default:''"`
	ReadMessages  bool   `json:"readMessages" gorm:"default:false"`
	IgnoreGroups  bool   `json:"ignoreGroups" gorm:"default:false"`
	IgnoreStatus  bool   `json:"ignoreStatus" gorm:"default:false"`

	// Calling (KRIONIX): answer inbound WhatsApp calls of this instance through
	// meowcaller (also needs CALLS_ENABLED=true). Off by default; only the global
	// API key can change it (PUT /calling/instances/:instanceId).
	CallsEnabled          bool       `json:"callsEnabled" gorm:"default:false"`
	CallsEnabledChangedAt *time.Time `json:"callsEnabledChangedAt,omitempty"`
	CallsEnabledChangedBy string     `json:"callsEnabledChangedBy,omitempty" gorm:"default:''"`
}

// AdvancedSettings representa as configurações avançadas de uma instância.
// Pointer fields preserve omitted/null keys during partial updates.
type AdvancedSettings struct {
	AlwaysOnline  *bool   `json:"alwaysOnline"`
	RejectCall    *bool   `json:"rejectCall"`
	MsgRejectCall *string `json:"msgRejectCall"`
	ReadMessages  *bool   `json:"readMessages"`
	IgnoreGroups  *bool   `json:"ignoreGroups"`
	IgnoreStatus  *bool   `json:"ignoreStatus"`
	// CallsEnabled is reported by GET; an instance-token PUT may only echo the
	// current value (changing it needs the global API key).
	CallsEnabled *bool `json:"callsEnabled"`
}

func (m *Instance) BeforeCreate(tx *gorm.DB) (err error) {
	if m.Id == "" {
		m.Id = uuid.New().String()
	}
	return
}

// BoolPtr returns a pointer to v (helper for AdvancedSettings responses/tests).
func BoolPtr(v bool) *bool {
	return &v
}
