//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	appoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	operatordomain "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGlobalPolicySnapshotHasSingleActiveVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	repository := policy.NewPostgresRepository(db)
	before, err := repository.FindGlobalActive(ctx)
	if err != nil {
		t.Fatalf("find bootstrap global policy: %v", err)
	}
	if before.Version < 1 || len(before.Document) == 0 {
		t.Fatalf("invalid bootstrap policy snapshot: %#v", before)
	}
	tx := beginDomainTx(t, ctx, db)
	locked, err := repository.FindGlobalActiveForUpdate(ctx, tx)
	if err != nil || locked.Version != before.Version {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("lock global policy: snapshot=%#v err=%v", locked, err)
	}
	after, err := repository.ActivateGlobal(ctx, tx, locked.Document, uuid.New(), "TEST_POLICY_SNAPSHOT")
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("activate global policy: %v", err)
	}
	commitDomainTx(t, ctx, tx)
	if after.Version <= before.Version || after.Status != "active" || string(after.Document) != string(before.Document) {
		t.Fatalf("unexpected activated snapshot: before=%#v after=%#v", before, after)
	}
	var active int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_policy_global_snapshots WHERE status='active'`).Scan(&active); err != nil {
		t.Fatalf("count active snapshots: %v", err)
	}
	if active != 1 {
		t.Fatalf("active global snapshots=%d, want 1", active)
	}
}

func TestPolicyUpdateCreatesOneGlobalSnapshotAndReplaysSameKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	operatorID := uuid.New()
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_user_auth_states (user_id, status, restriction_version, effective_at)
		VALUES ($1, 'active', 1, now())
	`, operatorID); err != nil {
		t.Fatalf("seed operator state: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_access_grants (
			access_grant_id, user_id, roles, permissions, grant_version, grant_status, source, source_revision, valid_from
		) VALUES ($1, $2, ARRAY['platform_operator'], ARRAY['auth.policy.read','auth.policy.write'], 1, 'active', 'test', 'test', now())
	`, uuid.New(), operatorID); err != nil {
		t.Fatalf("seed operator grant: %v", err)
	}
	keys := security.Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         []byte("01234567890123456789012345678901"),
		JWTIssuer:      "integration",
	}
	service := appoperator.NewService(
		db, keys, operatordomain.NewPostgresRepository(db), policy.NewPostgresRepository(db), access.NewPostgresRepository(db),
		idempotency.NewPostgresRepository(db), outbox.NewPostgresRepository(db), appoperator.Config{StrongAuthTTL: time.Minute}, appoperator.DenyApprovalPort{},
	)
	principal := appsession.Principal{Authenticated: true, SessionID: uuid.New(), UserID: operatorID, Channel: "web", Method: "email_password", AuthenticatedAt: time.Now().UTC(), GrantVersion: 1}
	before, err := service.PolicyView(ctx, principal)
	if err != nil {
		t.Fatalf("read global policy: %v", err)
	}
	key := uuid.NewString()
	input := appoperator.PolicyUpdateInput{
		Principal: principal, Name: "login-lock", IfMatch: fmt.Sprintf("\"policy-%d\"", before.Version), IdempotencyKey: key,
		Patch: map[string]any{"policyName": "login-lock", "failureThreshold": float64(6), "changeReason": "TEST_POLICY_UPDATE"},
	}
	updated, err := service.UpdatePolicy(ctx, input)
	if err != nil {
		t.Fatalf("update global policy: %v", err)
	}
	if updated.Version <= before.Version || updated.Name != "login-lock" || updated.Status != "active" {
		t.Fatalf("unexpected updated policy: %#v", updated)
	}
	replayed, err := service.UpdatePolicy(ctx, input)
	if err != nil {
		t.Fatalf("replay policy update: %v", err)
	}
	if replayed.Name != updated.Name || replayed.Version != updated.Version || replayed.Status != updated.Status || !replayed.EffectiveAt.Equal(updated.EffectiveAt) {
		t.Fatalf("replayed policy update=%#v, want %#v", replayed, updated)
	}
	after, err := service.PolicyView(ctx, principal)
	if err != nil {
		t.Fatalf("read updated global policy: %v", err)
	}
	if after.Version != updated.Version || after.LoginLock["failureThreshold"] != float64(6) {
		t.Fatalf("unexpected current global policy: %#v", after)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_policy_global_snapshots`).Scan(&count); err != nil {
		t.Fatalf("count global policy snapshots: %v", err)
	}
	if count != 2 {
		t.Fatalf("global snapshot count=%d, want 2", count)
	}

	concurrentInput := appoperator.PolicyUpdateInput{
		Principal: principal, Name: "login-lock", IfMatch: fmt.Sprintf("\"policy-%d\"", after.Version), IdempotencyKey: uuid.NewString(),
		Patch: map[string]any{"policyName": "login-lock", "lockSeconds": float64(901), "changeReason": "TEST_POLICY_CONCURRENT_REPLAY"},
	}
	type updateResult struct {
		value appoperator.PolicyUpdateOutput
		err   error
	}
	results := make(chan updateResult, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			value, updateErr := service.UpdatePolicy(ctx, concurrentInput)
			results <- updateResult{value: value, err: updateErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var concurrent []appoperator.PolicyUpdateOutput
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent policy update: %v", result.err)
		}
		concurrent = append(concurrent, result.value)
	}
	if len(concurrent) != 2 || concurrent[0].Name != concurrent[1].Name || concurrent[0].Version != concurrent[1].Version || concurrent[0].Status != concurrent[1].Status || !concurrent[0].EffectiveAt.Equal(concurrent[1].EffectiveAt) {
		t.Fatalf("concurrent policy results=%#v", concurrent)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_policy_global_snapshots`).Scan(&count); err != nil {
		t.Fatalf("count concurrent global policy snapshots: %v", err)
	}
	if count != 3 {
		t.Fatalf("global snapshot count after same-key concurrency=%d, want 3", count)
	}
}

func TestManualActionReplaysOriginalActionResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	operatorID := uuid.New()
	seedOperatorGrant(t, ctx, db, operatorID, []string{"auth.case.execute", "auth.identity_link.revoke"})
	targetUserID, identityID, linkID := uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, targetUserID, identityID, linkID)
	keys := testOperatorKeys()
	service := appoperator.NewService(
		db, keys, operatordomain.NewPostgresRepository(db), policy.NewPostgresRepository(db), access.NewPostgresRepository(db),
		idempotency.NewPostgresRepository(db), outbox.NewPostgresRepository(db), appoperator.Config{StrongAuthTTL: time.Minute}, appoperator.StaticApprovalPort{Allow: true},
	)
	principal := appsession.Principal{Authenticated: true, SessionID: uuid.New(), UserID: operatorID, Channel: "web", Method: "email_password", AuthenticatedAt: time.Now().UTC(), GrantVersion: 1}
	input := appoperator.ManualInput{
		Principal: principal, CaseID: "case-123", TargetType: "identity_link", TargetID: linkID.String(), Action: "revoke_identity_link",
		ReasonCode: "CUSTOMER_SUPPORT", ApprovalID: "approval-123", EvidenceRef: "evidence-123", ExpectedVersion: 0, IdempotencyKey: uuid.NewString(),
	}
	firstID, firstVersion, err := service.Manual(ctx, input)
	if err != nil {
		t.Fatalf("apply manual action: %v", err)
	}
	replayedID, replayedVersion, err := service.Manual(ctx, input)
	if err != nil {
		t.Fatalf("replay manual action: %v", err)
	}
	if replayedID != firstID || replayedVersion != firstVersion || firstVersion != 1 {
		t.Fatalf("manual replay first=(%s,%d) replay=(%s,%d)", firstID, firstVersion, replayedID, replayedVersion)
	}
	var actions int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_manual_actions`).Scan(&actions); err != nil {
		t.Fatalf("count manual actions: %v", err)
	}
	if actions != 1 {
		t.Fatalf("manual actions=%d, want 1", actions)
	}
}

func TestManualActionRepositoryTransactionIsAtomic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	operatorID, targetUserID, identityID, linkID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, targetUserID, identityID, linkID)
	keys := testOperatorKeys()
	idempotencyRepository := idempotency.NewPostgresRepository(db)
	operatorRepository := operatordomain.NewPostgresRepository(db)
	tx := beginDomainTx(t, ctx, db)
	actionID := uuid.New()
	record := idempotency.NewRecord("manual_auth_action", keys.Hash("manual_auth_action", operatorID.String()), keys.Hash("manual-key"), keys.Hash("revoke_identity_link", "identity_link", linkID.String(), "CUSTOMER_SUPPORT"), &actionID, nil, time.Now().UTC().Add(time.Hour))
	if err := idempotencyRepository.CreateProcessing(ctx, tx, record, "ManualAction"); err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("create manual idempotency record: %v", err)
	}
	version, err := operatorRepository.ApplyManual(ctx, tx, operatordomain.ManualAction{ID: actionID, OperatorID: operatorID, CaseID: "case-atomic", TargetType: "identity_link", TargetID: linkID.String(), Action: "revoke_identity_link", ReasonCode: "CUSTOMER_SUPPORT", ApprovalID: "approval-atomic", EvidenceRef: "evidence-atomic", ExpectedVersion: 0, IdempotencyID: &record.ID})
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("apply manual repository action: %v", err)
	}
	if err := idempotencyRepository.Complete(ctx, tx, record.ID, "completed"); err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("complete manual idempotency record: %v", err)
	}
	if err := outbox.NewPostgresRepository(db).Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.ManualActionCompleted", AggregateType: "ManualAction", AggregateID: actionID, Version: version, Payload: json.RawMessage(`{"status":"completed"}`), CorrelationID: uuid.New()}); err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("append manual domain outbox: %v", err)
	}
	if err := application.AppendAudit(ctx, tx, "auth.manual_action.completed", "operator", operatorID, actionID, map[string]string{"action": "revoke_identity_link"}, "manual-key"); err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("append manual audit outbox: %v", err)
	}
	commitDomainTx(t, ctx, tx)
}

func seedOperatorGrant(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID uuid.UUID, permissions []string) {
	t.Helper()
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_user_auth_states (user_id, status, restriction_version, effective_at)
		VALUES ($1, 'active', 1, now())
	`, userID); err != nil {
		t.Fatalf("seed operator state: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_access_grants (
			access_grant_id, user_id, roles, permissions, grant_version, grant_status, source, source_revision, valid_from
		) VALUES ($1, $2, ARRAY['platform_operator'], $3, 1, 'active', 'test', 'test', now())
	`, uuid.New(), userID, permissions); err != nil {
		t.Fatalf("seed operator grant: %v", err)
	}
}

func testOperatorKeys() security.Keys {
	return security.Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         []byte("01234567890123456789012345678901"),
		JWTIssuer:      "integration",
	}
}
