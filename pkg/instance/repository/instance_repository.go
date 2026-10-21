package instance_repository

import (
	"errors"
	"fmt"
	"time"

	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"github.com/gomessguii/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"

	label_model "github.com/evolution-foundation/evolution-go/pkg/label/model"
	label_repository "github.com/evolution-foundation/evolution-go/pkg/label/repository"

	message_model "github.com/evolution-foundation/evolution-go/pkg/message/model"
	message_repository "github.com/evolution-foundation/evolution-go/pkg/message/repository"
)

type InstanceRepository interface {
	Create(instance instance_model.Instance) (*instance_model.Instance, error)
	GetInstanceByID(instanceId string) (*instance_model.Instance, error)
	GetConnectedInstanceByID(instanceId string) (*instance_model.Instance, error)
	GetInstanceByToken(token string) (*instance_model.Instance, error)
	GetInstanceByName(name string) (*instance_model.Instance, error)
	Update(*instance_model.Instance) error
	UpdateConnected(userId string, status bool, disconnectReason string) error
	UpdateQrcode(userId string, qr string) error
	UpdateProxy(userId string, proxy string) error
	UpdateJid(userId string, jid string) error
	UpdateConnectSettings(instanceId string, updates map[string]interface{}) error
	GetAllConnectedInstances() ([]*instance_model.Instance, error)
	GetAllConnectedInstancesByClientName(clientName string) ([]*instance_model.Instance, error)
	GetAll(clientName string) ([]*instance_model.Instance, error)
	Delete(instanceId string) error
	GetAdvancedSettings(instanceId string) (*instance_model.AdvancedSettings, error)
	UpdateAdvancedSettings(instanceId string, settings *instance_model.AdvancedSettings) error
	ListCalling() ([]*instance_model.Instance, error)
	SetCallsEnabled(instanceId string, enabled bool, changedBy string, at time.Time) error
}

type instanceRepository struct {
	db          *gorm.DB
	labelRepo   label_repository.LabelRepository
	messageRepo message_repository.MessageRepository
}

func (i *instanceRepository) Create(instance instance_model.Instance) (*instance_model.Instance, error) {
	if err := i.db.Create(&instance).Error; err != nil {
		return nil, err
	}
	return &instance, nil
}

func (i *instanceRepository) GetInstanceByToken(token string) (*instance_model.Instance, error) {
	var instance instance_model.Instance
	err := i.db.Where("token = ?", token).First(&instance).Error
	if err != nil {
		return nil, err
	}

	return &instance, nil
}

func (i *instanceRepository) GetInstanceByName(name string) (*instance_model.Instance, error) {
	var instance instance_model.Instance
	err := i.db.Where("name = ?", name).First(&instance).Error
	if err != nil {
		return nil, err
	}

	return &instance, nil
}

func (i *instanceRepository) GetInstanceByID(instanceId string) (*instance_model.Instance, error) {
	// Valida o formato do UUID
	if _, err := uuid.Parse(instanceId); err != nil {
		return nil, fmt.Errorf("invalid UUID format: %v", err)
	}

	var instance instance_model.Instance
	err := i.db.Where("id = ?", instanceId).First(&instance).Error
	if err != nil {
		return nil, err
	}

	return &instance, nil
}

func (i *instanceRepository) GetConnectedInstanceByID(instanceId string) (*instance_model.Instance, error) {
	var instance instance_model.Instance
	err := i.db.Where("id = ? AND connected = ?", instanceId, true).First(&instance).Error
	if err != nil {
		return nil, err
	}

	return &instance, nil
}

func (i *instanceRepository) Update(instance *instance_model.Instance) error {
	err := i.db.Save(&instance).Error
	if err != nil {
		logger.LogError("Error updating instance in DB: %v", err)
	}
	return err
}

func (i *instanceRepository) UpdateConnected(userId string, status bool, disconnectReason string) error {
	return i.db.Model(&instance_model.Instance{}).Where("id = ?", userId).Update("connected", status).Update("disconnect_reason", disconnectReason).Error
}

func (i *instanceRepository) UpdateQrcode(userId string, qr string) error {
	return i.db.Model(&instance_model.Instance{}).Where("id = ?", userId).Update("qrcode", qr).Error
}

func (i *instanceRepository) UpdateProxy(userId string, proxy string) error {
	return i.db.Model(&instance_model.Instance{}).Where("id = ?", userId).Update("proxy", proxy).Error
}

func (i *instanceRepository) UpdateJid(userId string, jid string) error {
	return i.db.Model(&instance_model.Instance{}).Where("id = ?", userId).Update("jid", jid).Error
}

func (i *instanceRepository) UpdateConnectSettings(instanceId string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	result := i.db.Model(&instance_model.Instance{}).Where("id = ?", instanceId).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return instance_model.ErrInstanceNotFound
	}
	return nil
}

func (i *instanceRepository) GetAllConnectedInstances() ([]*instance_model.Instance, error) {
	var instances []*instance_model.Instance
	err := i.db.Where("connected = ?", true).Find(&instances).Error
	if err != nil {
		return nil, err
	}

	return instances, nil
}

func (i *instanceRepository) GetAllConnectedInstancesByClientName(clientName string) ([]*instance_model.Instance, error) {
	var instances []*instance_model.Instance
	err := i.db.Where("connected = ? AND client_name = ?", true, clientName).Find(&instances).Error
	if err != nil {
		return nil, err
	}

	return instances, nil
}

func (i *instanceRepository) GetAll(clientName string) ([]*instance_model.Instance, error) {
	var instances []*instance_model.Instance
	err := i.db.Where("client_name = ?", clientName).Find(&instances).Error
	if err != nil {
		return nil, err
	}

	return instances, nil
}

func (i *instanceRepository) Delete(instanceId string) error {
	return i.db.Transaction(func(tx *gorm.DB) error {
		// Deleta todas as labels associadas à instância
		if err := tx.Where("instance_id = ?", instanceId).Delete(&label_model.Label{}).Error; err != nil {
			return fmt.Errorf("erro ao deletar labels: %v", err)
		}

		// Deleta todas as mensagens associadas à instância
		if err := tx.Where("source = ?", instanceId).Delete(&message_model.Message{}).Error; err != nil {
			return fmt.Errorf("erro ao deletar mensagens: %v", err)
		}

		// Deleta a instância
		if err := tx.Where("id = ?", instanceId).Delete(&instance_model.Instance{}).Error; err != nil {
			return fmt.Errorf("erro ao deletar instância: %v", err)
		}

		return nil
	})
}

func (i *instanceRepository) GetAdvancedSettings(instanceId string) (*instance_model.AdvancedSettings, error) {
	// Valida o formato do UUID
	if _, err := uuid.Parse(instanceId); err != nil {
		return nil, fmt.Errorf("invalid UUID format: %v", err)
	}

	var instance instance_model.Instance
	err := i.db.Select("always_online, reject_call, msg_reject_call, read_messages, ignore_groups, ignore_status, calls_enabled").
		Where("id = ?", instanceId).First(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, instance_model.ErrInstanceNotFound
		}
		return nil, err
	}

	settings := &instance_model.AdvancedSettings{
		AlwaysOnline:  instance_model.BoolPtr(instance.AlwaysOnline),
		RejectCall:    instance_model.BoolPtr(instance.RejectCall),
		MsgRejectCall: &instance.MsgRejectCall,
		ReadMessages:  instance_model.BoolPtr(instance.ReadMessages),
		IgnoreGroups:  instance_model.BoolPtr(instance.IgnoreGroups),
		IgnoreStatus:  instance_model.BoolPtr(instance.IgnoreStatus),
		CallsEnabled:  instance_model.BoolPtr(instance.CallsEnabled),
	}

	return settings, nil
}

func (i *instanceRepository) UpdateAdvancedSettings(instanceId string, settings *instance_model.AdvancedSettings) error {
	// Valida o formato do UUID
	if _, err := uuid.Parse(instanceId); err != nil {
		return fmt.Errorf("invalid UUID format: %v", err)
	}

	updates := buildAdvancedSettingsUpdates(settings)
	if len(updates) == 0 {
		return nil
	}

	result := i.db.Model(&instance_model.Instance{}).Where("id = ?", instanceId).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return instance_model.ErrInstanceNotFound
	}

	return nil
}

// buildAdvancedSettingsUpdates writes only non-nil fields, including explicit
// false flags and an empty reject message. Omitted/null fields stay unchanged.
func buildAdvancedSettingsUpdates(settings *instance_model.AdvancedSettings) map[string]interface{} {
	updates := map[string]interface{}{}
	if settings == nil {
		return updates
	}
	if settings.AlwaysOnline != nil {
		updates["always_online"] = *settings.AlwaysOnline
	}
	if settings.RejectCall != nil {
		updates["reject_call"] = *settings.RejectCall
	}
	if settings.ReadMessages != nil {
		updates["read_messages"] = *settings.ReadMessages
	}
	if settings.IgnoreGroups != nil {
		updates["ignore_groups"] = *settings.IgnoreGroups
	}
	if settings.IgnoreStatus != nil {
		updates["ignore_status"] = *settings.IgnoreStatus
	}
	if settings.MsgRejectCall != nil {
		updates["msg_reject_call"] = *settings.MsgRejectCall
	}
	return updates
}

// ListCalling returns every instance with only the fields the calling admin needs
// (never tokens, JIDs or QR codes).
func (i *instanceRepository) ListCalling() ([]*instance_model.Instance, error) {
	var instances []*instance_model.Instance
	err := i.db.Select("id, name, connected, client_name, reject_call, calls_enabled, calls_enabled_changed_at, calls_enabled_changed_by").
		Order("name").Find(&instances).Error
	return instances, err
}

// SetCallsEnabled persists the per-instance calling switch with its audit fields.
// Advanced-settings updates never write calls_enabled.
func (i *instanceRepository) SetCallsEnabled(instanceId string, enabled bool, changedBy string, at time.Time) error {
	if _, err := uuid.Parse(instanceId); err != nil {
		return fmt.Errorf("invalid UUID format: %v", err)
	}
	result := i.db.Model(&instance_model.Instance{}).Where("id = ?", instanceId).Updates(map[string]interface{}{
		"calls_enabled":            enabled,
		"calls_enabled_changed_at": at,
		"calls_enabled_changed_by": changedBy,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return instance_model.ErrInstanceNotFound
	}
	return nil
}

func NewInstanceRepository(db *gorm.DB) InstanceRepository {
	return &instanceRepository{
		db: db,
	}
}
