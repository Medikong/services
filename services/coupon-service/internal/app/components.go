package app

import (
	"context"
	"strings"
	"time"

	campaignapp "github.com/Medikong/services/services/coupon-service/internal/application/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/application/commanding"
	issuanceapp "github.com/Medikong/services/services/coupon-service/internal/application/issuance"
	"github.com/Medikong/services/services/coupon-service/internal/application/operations"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	redemptionapp "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	"github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	domainoperations "github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/Medikong/services/services/coupon-service/internal/platform/external"
	"github.com/samber/oops"
)

type externalPorts struct {
	authorization  ports.WorkloadAuthorizationPort
	commandSource  commanding.OperationsCommandSource
	users          ports.UserEligibilityPort
	products       ports.ProductSnapshotPort
	drops          ports.DropSnapshotPort
	sellers        ports.SellerCatalogSnapshotPort
	orders         ports.OrderSnapshotPort
	payments       ports.PaymentResultPort
	cases          ports.CSCasePort
	approvals      ports.OperationApprovalPort
	audience       ports.BulkAudiencePort
	settlement     ports.SettlementEventPort
	notifications  ports.NotificationEventPort
	observability  ports.ObservabilityPort
	replayPayloads redemptionapp.ReplayPayloadStore
}

// ExternalDependencies is the deployment composition boundary for data owned
// by other bounded contexts. Coupon code stores only verified references and
// immutable snapshots returned through these ports.
type ExternalDependencies struct {
	WorkloadAuthorization ports.WorkloadAuthorizationPort
	OperationsCommands    commanding.OperationsCommandSource
	UserEligibility       ports.UserEligibilityPort
	ProductSnapshots      ports.ProductSnapshotPort
	DropSnapshots         ports.DropSnapshotPort
	SellerCatalog         ports.SellerCatalogSnapshotPort
	OrderSnapshots        ports.OrderSnapshotPort
	PaymentResults        ports.PaymentResultPort
	CSCases               ports.CSCasePort
	OperationApprovals    ports.OperationApprovalPort
	BulkAudience          ports.BulkAudiencePort
	SettlementEvents      ports.SettlementEventPort
	NotificationEvents    ports.NotificationEventPort
	Observability         ports.ObservabilityPort
	ReplayPayloads        redemptionapp.ReplayPayloadStore
}

func (d ExternalDependencies) resolve() (externalPorts, error) {
	if d.WorkloadAuthorization == nil || d.UserEligibility == nil || d.ProductSnapshots == nil || d.DropSnapshots == nil ||
		d.SellerCatalog == nil || d.OrderSnapshots == nil || d.PaymentResults == nil ||
		d.CSCases == nil || d.OperationApprovals == nil || d.BulkAudience == nil ||
		d.SettlementEvents == nil || d.NotificationEvents == nil || d.Observability == nil || d.ReplayPayloads == nil {
		return externalPorts{}, oops.In("coupon_components").Code("coupon.external_dependencies_required").New("all external context ports are required")
	}
	return externalPorts{
		authorization: d.WorkloadAuthorization,
		commandSource: d.OperationsCommands,
		users:         d.UserEligibility, products: d.ProductSnapshots, drops: d.DropSnapshots,
		sellers: d.SellerCatalog, orders: d.OrderSnapshots, payments: d.PaymentResults,
		cases: d.CSCases, approvals: d.OperationApprovals, audience: d.BulkAudience,
		settlement: d.SettlementEvents, notifications: d.NotificationEvents,
		observability: d.Observability, replayPayloads: d.ReplayPayloads,
	}, nil
}

func unavailableExternalPorts() externalPorts {
	adapter := external.Unavailable{}
	return externalPorts{
		authorization: adapter,
		commandSource: adapter,
		users:         adapter, products: adapter, drops: adapter, sellers: adapter, orders: adapter,
		payments: adapter, cases: adapter, approvals: adapter, audience: adapter,
		settlement: adapter, notifications: adapter, observability: adapter, replayPayloads: adapter,
	}
}

func allowUnavailableExternalDependencies(environment string) error {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "local", "development", "dev", "test":
		return nil
	default:
		return oops.In("coupon_components").
			Code("coupon.external_dependencies_injection_required").
			With("environment", environment).
			New("external dependencies must be injected outside local development")
	}
}

type components struct {
	campaigns      *campaignapp.Service
	issuance       *issuanceapp.Service
	redemptions    *redemptionapp.Service
	operations     *operations.Service
	campaignRepo   campaign.Repository
	issueRepo      issuerequest.Repository
	codeRepo       couponcode.Repository
	userCouponRepo usercoupon.Repository
	redemptionRepo redemption.Repository
	bulkRepo       bulk.Repository
	controlRepo    domainoperations.Repository
	recoveryRepo   recovery.Repository
	readRepo       readmodel.Repository
	commandIngress *commanding.OperationsIngress
	external       externalPorts
}

func newComponents(resources Resources, policy config.DomainPolicyConfig, externalPorts externalPorts) (components, error) {
	if resources.DB == nil {
		return components{}, oops.In("coupon_components").Code("coupon.database_required").New("postgres pool is required")
	}
	campaignRepo, err := campaign.NewPostgresRepository(resources.DB)
	if err != nil {
		return components{}, err
	}
	issueRepo, err := issuerequest.NewPostgresRepository(resources.DB)
	if err != nil {
		return components{}, err
	}
	codeRepo, err := couponcode.NewPostgresRepository(resources.DB)
	if err != nil {
		return components{}, err
	}
	userCouponRepo, err := usercoupon.NewPostgresRepository(resources.DB)
	if err != nil {
		return components{}, err
	}
	readRepo, err := readmodel.NewPostgresRepository(resources.DB)
	if err != nil {
		return components{}, err
	}
	redemptionRepo := redemption.NewPostgresRepository(resources.DB)
	bulkRepo := bulk.NewPostgresRepository(resources.DB)
	controlRepo := domainoperations.NewPostgresRepository(resources.DB)
	recoveryRepo := recovery.NewPostgresRepository(resources.DB)
	commandIngress, err := commanding.NewOperationsIngress(eventing.NewPostgresCommandQueue(resources.DB))
	if err != nil {
		return components{}, err
	}

	campaignService, err := campaignapp.New(campaignapp.Dependencies{
		Repository: campaignRepo, SellerSnapshots: externalPorts.sellers, Approvals: externalPorts.approvals,
	})
	if err != nil {
		return components{}, err
	}
	issuanceService, err := issuanceapp.New(issuanceapp.Dependencies{
		Campaigns: campaignRepo, IssueRequests: issueRepo, Codes: codeRepo, UserCoupons: userCouponRepo,
		Approvals: externalPorts.approvals, Cases: externalPorts.cases, UserEligibility: externalPorts.users,
		OperationalControl: controlRepo,
		CodeHashKey:        []byte(policy.CodeHashKey), CodeReservationTTL: policy.CodeReservationTTL,
	})
	if err != nil {
		return components{}, err
	}
	redemptionService, err := redemptionapp.NewService(redemptionapp.Dependencies{
		Redemptions: redemptionRepo, UserCoupons: userCouponRepo, Campaigns: campaignRepo, Controls: controlRepo,
		Users: externalPorts.users, Products: externalPorts.products, Drops: externalPorts.drops,
		Sellers: externalPorts.sellers, Orders: externalPorts.orders, Payments: externalPorts.payments,
		Cases: externalPorts.cases, ReplayPayloads: externalPorts.replayPayloads,
	}, policy.ReservationTTL, func() time.Time { return time.Now().UTC() })
	if err != nil {
		return components{}, err
	}
	operationsService, err := operations.NewService(operations.Dependencies{
		BulkJobs: bulkRepo, Controls: controlRepo, Recoveries: recoveryRepo, UserCoupons: userCouponRepo,
		Redemptions: redemptionRepo,
		Audience:    externalPorts.audience, Approvals: externalPorts.approvals, Cases: externalPorts.cases,
	})
	if err != nil {
		return components{}, err
	}
	return components{
		campaigns: campaignService, issuance: issuanceService, redemptions: redemptionService,
		operations: operationsService, campaignRepo: campaignRepo, issueRepo: issueRepo,
		codeRepo: codeRepo, userCouponRepo: userCouponRepo, redemptionRepo: redemptionRepo,
		bulkRepo: bulkRepo, controlRepo: controlRepo, recoveryRepo: recoveryRepo, readRepo: readRepo,
		commandIngress: commandIngress, external: externalPorts,
	}, nil
}

func newHTTPBackend(_ context.Context, resources Resources, cfg config.ServerConfig) (*httpBackend, error) {
	return newHTTPBackendWithPorts(resources, cfg, unavailableExternalPorts())
}

func newHTTPBackendWithPorts(resources Resources, cfg config.ServerConfig, external externalPorts) (*httpBackend, error) {
	parts, err := newComponents(resources, cfg.Policy, external)
	if err != nil {
		return nil, err
	}
	return &httpBackend{components: parts, resources: resources, policy: cfg.Policy, now: func() time.Time { return time.Now().UTC() }}, nil
}
