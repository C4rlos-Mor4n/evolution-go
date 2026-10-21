//go:build integration

package instance_repository

import (
	"os"
	"testing"
	"time"

	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Run against a real Postgres that already holds instances created before the
// calling switch existed: KX_IT_DSN=... go test -tags integration ./pkg/instance/repository/
func TestCallingSwitchOnRealPostgres(t *testing.T) {
	dsn := os.Getenv("KX_IT_DSN")
	if dsn == "" {
		t.Skip("KX_IT_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	repo := &instanceRepository{db: db}
	const a, b = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"

	list, err := repo.ListCalling()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected the 2 pre-existing instances, got %d", len(list))
	}
	for _, inst := range list {
		if inst.CallsEnabled {
			t.Fatalf("pre-existing instance %s migrated with calling ON; default must be off", inst.Name)
		}
		if inst.Token != "" || inst.Jid != "" || inst.Qrcode != "" {
			t.Fatal("ListCalling must not load tokens, JIDs or QR codes")
		}
	}

	at := time.Now().UTC().Truncate(time.Second)
	if err := repo.SetCallsEnabled(b, true, "global_api_key:it", at); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetCallsEnabled("33333333-3333-4333-8333-333333333333", true, "x", at); err != instance_model.ErrInstanceNotFound {
		t.Fatalf("unknown instance: got %v, want ErrInstanceNotFound", err)
	}
	gotB, _ := repo.GetInstanceByID(b)
	gotA, _ := repo.GetInstanceByID(a)
	if !gotB.CallsEnabled || gotB.CallsEnabledChangedBy != "global_api_key:it" || gotB.CallsEnabledChangedAt == nil {
		t.Fatalf("B not persisted with audit: %+v", gotB)
	}
	if gotA.CallsEnabled {
		t.Fatal("enabling B changed A")
	}
	settings, err := repo.GetAdvancedSettings(b)
	if err != nil || settings.CallsEnabled == nil || !*settings.CallsEnabled {
		t.Fatalf("advanced settings must report callsEnabled=true: %+v %v", settings, err)
	}
	// An advanced-settings update never touches the switch.
	off := false
	if err := repo.UpdateAdvancedSettings(b, &instance_model.AdvancedSettings{AlwaysOnline: &off, CallsEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	gotB, _ = repo.GetInstanceByID(b)
	if !gotB.CallsEnabled {
		t.Fatal("UpdateAdvancedSettings wrote calls_enabled")
	}
}
