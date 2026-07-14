package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/samber/oops"

	campaignapp "github.com/Medikong/services/services/coupon-service/internal/application/campaign"
	issuanceapp "github.com/Medikong/services/services/coupon-service/internal/application/issuance"
	"github.com/Medikong/services/services/coupon-service/internal/application/operations"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	redemptionapp "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	domainoperations "github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	domainredemption "github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

const (
	issueStatusPath    = "/api/v1/users/me/coupons?status=pending"
	recoveryStatusPath = "/api/v1/internal/coupon-event-recoveries?status=retry_pending"
	asyncRetrySeconds  = 1
)

type httpBackend struct {
	components components
	resources  Resources
	policy     config.DomainPolicyConfig
	now        func() time.Time
}

func (b *httpBackend) Campaign(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	if err := b.authorizeWorkload(ctx, call); err != nil {
		return couponhttp.Result{}, err
	}
	switch call.OperationID {
	case "API.A.19-10":
		request, ok := call.Body.(*couponhttp.CreateCouponCampaignRequest)
		if !ok {
			return backendInvariant()
		}
		startsAt, err := parseTime(request.UsableFrom)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		endsAt, err := parseTime(request.ExpiresAt)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		owner, err := domainSnapshot(request.OwnerSnapshot)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		applicability, err := campaignApplicability(request.Applicability, startsAt)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		businessKey := commandBusinessKey(call, request.ExternalBusinessRef)
		approvalPolicy, err := domainSnapshot(request.ApprovalPolicy)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		var templateRef *shared.ExternalRef
		if request.TemplateRef != nil {
			value := domainExternal(*request.TemplateRef)
			templateRef = &value
		}
		result, err := b.components.campaigns.RegisterPolicy(ctx, campaignapp.RegisterPolicyInput{
			Metadata:            b.campaignMetadata(call, campaignapp.CommandRegisterPolicy, businessKey),
			DisplayName:         request.DisplayName,
			Description:         request.Description,
			StartsAt:            startsAt,
			EndsAt:              endsAt,
			Benefits:            []campaign.Benefit{campaignBenefit(request.Benefit)},
			Applicability:       applicability,
			IssuerAndFunding:    campaignFunding(request.IssuerAndFunding),
			OwnerSnapshot:       owner,
			ApprovalPolicy:      approvalPolicy,
			TemplateRef:         templateRef,
			ExternalBusinessRef: request.ExternalBusinessRef,
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		response, err := b.campaignResponse(ctx, result.CampaignID)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{
			Data: response, Location: "/api/v1/internal/coupon-campaigns/" + result.CampaignID,
		}, nil

	case "API.A.19-11":
		request, ok := call.Body.(*couponhttp.ConfigureIssuanceRequest)
		if !ok {
			return backendInvariant()
		}
		claimStartsAt, err := parseTime(request.ClaimStartsAt)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		claimEndsAt, err := parseTime(request.ClaimEndsAt)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		campaignID := call.Path["campaignId"]
		_, err = b.components.campaigns.ConfigureFirstComeLimit(ctx, campaignapp.ConfigureFirstComeLimitInput{
			Metadata:        b.campaignMetadata(call, campaignapp.CommandConfigureFirstComeLimit, commandBusinessKey(call, campaignID)),
			CampaignID:      campaignID,
			ExpectedVersion: *request.ExpectedVersion,
			Limit: campaign.QuantityLimit{
				TotalQuantity: *request.TotalQuantity, PerUserLimit: *request.PerUserLimit,
				ClaimStartsAt: claimStartsAt, ClaimEndsAt: claimEndsAt,
			},
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return b.currentCampaignResult(ctx, campaignID)

	case "API.A.19-12":
		request, ok := call.Body.(*couponhttp.ReviewCampaignRequest)
		if !ok {
			return backendInvariant()
		}
		snapshot, err := domainSnapshot(request.SellerOwnershipSnapshot)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		campaignID := call.Path["campaignId"]
		_, err = b.components.campaigns.ReviewSellerCoupon(ctx, campaignapp.ReviewSellerCouponInput{
			Metadata:                b.campaignMetadata(call, campaignapp.CommandReviewSellerCoupon, commandBusinessKey(call, campaignID)),
			CampaignID:              campaignID,
			ExpectedVersion:         *request.ExpectedVersion,
			Decision:                campaign.Status(request.Decision),
			ReasonCode:              request.ReasonCode,
			SellerOwnershipSnapshot: snapshot,
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return b.currentCampaignResult(ctx, campaignID)

	case "API.A.19-13":
		request, ok := call.Body.(*couponhttp.CreatePolicyVersionRequest)
		if !ok {
			return backendInvariant()
		}
		effectiveAt, err := parseTime(request.EffectiveAt)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		input := campaignapp.ChangePolicyInput{
			CampaignID:      call.Path["campaignId"],
			ExpectedVersion: *request.ExpectedVersion,
			EffectiveAt:     effectiveAt,
		}
		input.Metadata = b.campaignMetadata(call, campaignapp.CommandChangePolicy, commandBusinessKey(call, input.CampaignID))
		if request.Benefit != nil {
			input.Benefits = []campaign.Benefit{campaignBenefit(*request.Benefit)}
		}
		if request.Applicability != nil {
			input.Applicability, err = campaignApplicability(*request.Applicability, effectiveAt)
			if err != nil {
				return couponhttp.Result{}, transportError(err)
			}
		}
		if request.IssuerAndFunding != nil {
			funding := campaignFunding(*request.IssuerAndFunding)
			input.IssuerAndFunding = &funding
		}
		if _, err := b.components.campaigns.ChangePolicy(ctx, input); err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return b.currentCampaignResult(ctx, input.CampaignID)

	case "API.A.19-16":
		return b.campaignPerformance(ctx, call)
	default:
		return backendInvariant()
	}
}

func (b *httpBackend) Issuance(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	if err := b.authorizeWorkload(ctx, call); err != nil {
		return couponhttp.Result{}, err
	}
	switch call.OperationID {
	case "API.A.19-01":
		userID, err := publicUserID(call)
		if err != nil {
			return couponhttp.Result{}, err
		}
		campaignID := call.Path["campaignId"]
		result, err := b.components.issuance.Claim(ctx, issuanceapp.ClaimInput{
			Metadata:   b.issuanceMetadata(call, issuanceapp.CommandClaim, commandBusinessKey(call, userID, campaignID)),
			CampaignID: campaignID,
			UserID:     userID,
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return issueAcceptedResult(result.IssueRequestID), nil

	case "API.A.19-02":
		request, ok := call.Body.(*couponhttp.RedeemCouponCodeRequest)
		if !ok {
			return backendInvariant()
		}
		userID, err := publicUserID(call)
		if err != nil {
			return couponhttp.Result{}, err
		}
		fingerprint, _, err := couponcode.Fingerprint(request.Code, []byte(b.policy.CodeHashKey))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		result, err := b.components.issuance.RedeemCode(ctx, issuanceapp.RedeemCodeInput{
			Metadata: b.issuanceMetadata(call, issuanceapp.CommandRedeemCode,
				commandBusinessKey(call, userID, hex.EncodeToString(fingerprint[:]))),
			UserID: userID,
			Code:   request.Code,
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		if result.Rejected {
			return couponhttp.Result{}, transportError(oops.In("coupon_http_backend").Code("coupon.code_rejected").
				With("reason_code", result.ReasonCode).New("coupon code was rejected"))
		}
		return issueAcceptedResult(result.IssueRequestID), nil

	case "API.A.19-03":
		return b.listWallet(ctx, call)

	case "API.A.19-04":
		return b.getCouponDetail(ctx, call)

	case "API.A.19-23":
		request, ok := call.Body.(*couponhttp.CreateCompensationIssueRequest)
		if !ok {
			return backendInvariant()
		}
		approvalPolicy, err := domainSnapshot(request.ApprovalPolicy)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		result, err := b.components.issuance.CreateCompensationIssueRequest(ctx, issuanceapp.CreateCompensationIssueRequestInput{
			Metadata: b.issuanceMetadata(call, issuanceapp.CommandCreateIssueRequest,
				commandBusinessKey(call, request.CampaignID, request.UserID, request.SourceRef.ID, call.Headers.CaseRef)),
			CampaignID:     request.CampaignID,
			UserID:         request.UserID,
			SourceRef:      domainExternal(request.SourceRef),
			ReasonCode:     request.ReasonCode,
			CaseRef:        call.Headers.CaseRef,
			ApprovalPolicy: approvalPolicy,
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return issueAcceptedResult(result.IssueRequestID), nil

	default:
		return backendInvariant()
	}
}

func (b *httpBackend) Redemption(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	if err := b.authorizeWorkload(ctx, call); err != nil {
		return couponhttp.Result{}, err
	}
	switch call.OperationID {
	case "API.A.19-05":
		request, ok := call.Body.(*couponhttp.ValidateCouponRequest)
		if !ok {
			return backendInvariant()
		}
		order, err := redemptionOrder(request.OrderSnapshot)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		metadata := b.redemptionMetadata(call, commandBusinessKey(call,
			request.OrderSnapshot.OrderID, request.UserCouponID, request.OrderSnapshot.SnapshotRef.SourceVersion))
		result, err := b.components.redemptions.Validate(ctx, redemptionapp.ValidateInput{
			UserCouponID:      request.UserCouponID,
			Order:             order,
			PolicyVersion:     *request.PolicyVersion,
			StackingPolicyRef: request.StackingPolicyRef,
			EvaluatedAt:       metadata.RequestedAt,
		}, metadata)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{Data: validationData(result, request.OrderSnapshot.SnapshotRef)}, nil

	case "API.A.19-06":
		request, ok := call.Body.(*couponhttp.ExpectedVersionRequest)
		if !ok {
			return backendInvariant()
		}
		redemptionID := call.Path["redemptionId"]
		result, err := b.components.redemptions.Reserve(ctx, redemptionapp.ReserveInput{
			RedemptionID: redemptionID, ExpectedVersion: *request.ExpectedVersion,
		}, b.redemptionMetadata(call, commandBusinessKey(call, redemptionID)))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{Data: redemptionData(result)}, nil

	case "API.A.19-07", "API.A.19-08", "API.A.19-09":
		request, ok := call.Body.(*couponhttp.RedemptionTransitionRequest)
		if !ok {
			return backendInvariant()
		}
		redemptionID := call.Path["redemptionId"]
		input, err := redemptionTransition(redemptionID, request)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		metadata := b.redemptionMetadata(call, commandBusinessKey(call, redemptionID))
		var result domainredemption.Redemption
		switch call.OperationID {
		case "API.A.19-07":
			result, err = b.components.redemptions.Confirm(ctx, input, metadata)
		case "API.A.19-08":
			result, err = b.components.redemptions.Release(ctx, input, metadata)
		case "API.A.19-09":
			if call.Headers.ApprovalRef != "" {
				if approvalErr := b.components.external.approvals.VerifyApproval(ctx, call.Headers.ApprovalRef, "CMD.A.19-15"); approvalErr != nil {
					return couponhttp.Result{}, transportError(approvalErr)
				}
			}
			result, err = b.components.redemptions.Reclaim(ctx, input, metadata)
		}
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{Data: redemptionData(result)}, nil

	default:
		return backendInvariant()
	}
}

func (b *httpBackend) Operations(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	if err := b.authorizeWorkload(ctx, call); err != nil {
		return couponhttp.Result{}, err
	}
	switch call.OperationID {
	case "API.A.19-14":
		request, ok := call.Body.(*couponhttp.CreateBulkIssueJobRequest)
		if !ok {
			return backendInvariant()
		}
		audienceSnapshot, err := domainSnapshot(request.AudienceSnapshot)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		evaluationAsOf, err := parseTime(request.EvaluationAsOf)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		job, err := b.components.operations.RegisterBulkJob(ctx, operations.RegisterBulkJobInput{
			CampaignID:       request.CampaignID,
			OwnerServiceID:   call.Principal.ServiceID,
			AudienceSnapshot: audienceSnapshot,
			EvaluationAsOf:   evaluationAsOf,
			OperationTaskRef: domainExternal(request.OperationTaskRef),
			ApprovalRef:      call.Headers.ApprovalRef,
		}, b.operationsMetadata(call, commandBusinessKey(call,
			request.CampaignID, request.OperationTaskRef.ID, request.AudienceSnapshot.PayloadHash)))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		path := "/api/v1/internal/bulk-coupon-issue-jobs/" + job.ID
		return couponhttp.Result{
			Data:     couponhttp.BulkJobAcceptedData{BulkJobID: job.ID, Status: "registered", StatusPath: path},
			Location: path, RetryAfterSeconds: asyncRetrySeconds,
		}, nil

	case "API.A.19-15":
		job, err := b.components.bulkRepo.Find(ctx, call.Path["bulkJobId"])
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		if err := b.authorizeWorkloadResources(ctx, call, map[string]string{
			"ownerServiceId": job.OwnerServiceID, "operationRequestRef": job.OperationRequestRef,
		}); err != nil {
			return couponhttp.Result{}, obscureBulkJobAuthorization(err)
		}
		summary, err := b.components.readRepo.BulkJobSummary(ctx, job.ID)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		data := bulkJobData(job)
		data.Counts.Reserved = summary.Counts.Reserved
		data.Counts.Confirmed = summary.Counts.Confirmed
		data.Counts.Released = summary.Counts.Released
		data.Counts.Reclaimed = summary.Counts.Reclaimed
		data.NextCursorRef = summary.NextCursorRef
		return couponhttp.Result{Data: data, AsOf: formatTime(later(job.UpdatedAt, summary.AsOf))}, nil

	case "API.A.19-17":
		request, ok := call.Body.(*couponhttp.ApplyOperationalControlRequest)
		if !ok {
			return backendInvariant()
		}
		effectiveFrom, err := parseTime(request.EffectiveFrom)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		control, err := b.components.operations.ApplyOperationalStop(ctx, operations.ApplyOperationalStopInput{
			Scopes:           []domainoperations.Scope{{Type: domainoperations.ScopeType(request.Scope.Type), Ref: request.Scope.Ref.ID}},
			Active:           *request.Active,
			EffectiveFrom:    effectiveFrom,
			BlockIssuance:    *request.BlockIssuance,
			BlockRedemption:  *request.BlockRedemption,
			OperationTaskRef: domainExternal(request.OperationTaskRef),
			ApprovalRef:      call.Headers.ApprovalRef,
			ReasonCode:       request.ReasonCode,
		}, b.operationsMetadata(call, commandBusinessKey(call,
			request.OperationTaskRef.ID, request.Scope.Type, request.Scope.Ref.ID, request.EffectiveFrom)))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		path := "/api/v1/internal/coupon-operational-controls/" + control.ID
		return couponhttp.Result{Data: operationalControlData(control, request.Scope.Ref), Location: path}, nil

	case "API.A.19-18":
		request, ok := call.Body.(*couponhttp.ApplyReadOnlyNoticeRequest)
		if !ok {
			return backendInvariant()
		}
		controlID := call.Path["controlId"]
		current, err := b.components.controlRepo.Find(ctx, controlID)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		effectiveFrom, err := parseTime(request.EffectiveFrom)
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		control, err := b.components.operations.ApplyReadOnlyNotice(ctx, operations.ApplyReadOnlyNoticeInput{
			ControlID:        controlID,
			ExpectedVersion:  *request.ExpectedVersion,
			Message:          request.Message,
			EffectiveFrom:    effectiveFrom,
			Active:           *request.Active,
			OperationTaskRef: shared.ExternalRef{Context: "operations", Type: "task", ID: current.OperationRequestRef},
			ApprovalRef:      call.Headers.ApprovalRef,
		}, b.operationsMetadata(call, commandBusinessKey(call, controlID)))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{Data: readOnlyNoticeData(control)}, nil

	case "API.A.19-19":
		return b.listRecoveries(ctx, call)

	case "API.A.19-20":
		request, ok := call.Body.(*couponhttp.RetryRecoveryRequest)
		if !ok {
			return backendInvariant()
		}
		recoveryID := call.Path["recoveryId"]
		requestedAt := b.currentTime()
		updated, err := b.components.operations.RequestRecovery(ctx, operations.RequestRecoveryInput{
			RecoveryID:       recoveryID,
			ReasonCode:       request.ReasonCode,
			NextAttemptAt:    requestedAt,
			OperationTaskRef: domainExternal(request.OperationTaskRef),
			ApprovalRef:      call.Headers.ApprovalRef,
		}, b.operationsMetadataAt(call,
			commandBusinessKey(call, recoveryID, request.OperationTaskRef.ID), requestedAt))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{
			Data: couponhttp.RecoveryAttemptAcceptedData{
				RecoveryID: updated.ID, AttemptID: updated.CurrentAttemptID,
				Status: "retry_pending", StatusPath: recoveryStatusPath,
			},
			Location: recoveryStatusPath, RetryAfterSeconds: asyncRetrySeconds,
		}, nil

	case "API.A.19-21":
		request, ok := call.Body.(*couponhttp.FinalizeRecoveryRequest)
		if !ok {
			return backendInvariant()
		}
		recoveryID := call.Path["recoveryId"]
		result, err := b.components.operations.FinalizeRecovery(ctx, operations.FinalizeRecoveryInput{
			RecoveryID:       recoveryID,
			ReasonCode:       request.ReasonCode,
			OperationTaskRef: domainExternal(request.OperationTaskRef),
			ApprovalRef:      call.Headers.ApprovalRef,
		}, b.operationsMetadata(call, commandBusinessKey(call, recoveryID, request.OperationTaskRef.ID)))
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		return couponhttp.Result{Data: recoveryAggregateItem(result), AsOf: formatTime(result.UpdatedAt)}, nil

	case "API.A.19-22":
		if err := b.components.external.cases.VerifyCase(ctx, call.Headers.CaseRef, ports.CSCaseBinding{UserID: call.Path["userId"]}); err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		limit, err := queryLimit(call)
		if err != nil {
			return couponhttp.Result{}, err
		}
		page, err := b.components.readRepo.ListTimeline(ctx, readmodel.TimelineQuery{
			UserID: call.Path["userId"], Cursor: call.Query.Get("cursor"), Limit: limit,
		})
		if err != nil {
			return couponhttp.Result{}, transportError(err)
		}
		items := make([]couponhttp.TimelineEvent, 0, len(page.Items))
		var asOf time.Time
		for _, item := range page.Items {
			items = append(items, couponhttp.TimelineEvent{
				EventID: item.TimelineID.String(), EventType: item.EventType,
				OccurredAt: formatTime(item.OccurredAt), UserCouponID: item.UserCouponID,
				ResultRef: readExternal(item.ResultRef),
			})
			asOf = later(asOf, item.OccurredAt)
		}
		return couponhttp.Result{Data: couponhttp.TimelineData{Items: items, NextCursor: page.NextCursor}, AsOf: formatTime(asOf)}, nil

	case "API.A.19-24":
		return b.incidentStatus(ctx)

	case "API.A.19-25":
		return b.listCostAttributions(ctx, call)

	default:
		return backendInvariant()
	}
}

func (b *httpBackend) authorizeWorkload(ctx context.Context, call couponhttp.Call) error {
	return b.authorizeWorkloadResources(ctx, call, nil)
}

func (b *httpBackend) authorizeWorkloadResources(ctx context.Context, call couponhttp.Call, additional map[string]string) error {
	if call.Principal.Type != principal.TypeService {
		return nil
	}
	if b.components.external.authorization == nil {
		return dependencyUnavailable(errors.New("workload authorization port is not configured"))
	}
	resources := make(map[string]string, len(call.Path)+len(call.Query)+len(additional))
	for name, value := range call.Path {
		resources[name] = value
	}
	for name, values := range call.Query {
		if len(values) == 1 {
			resources["query."+name] = values[0]
		}
	}
	for name, value := range additional {
		resources[name] = value
	}
	if err := b.components.external.authorization.AuthorizeWorkload(ctx, ports.WorkloadAccess{
		ServiceID: call.Principal.ServiceID, Roles: append([]string(nil), call.Principal.Roles...),
		OperationID: call.OperationID, Resources: resources,
	}); err != nil {
		return transportError(err)
	}
	return nil
}

func obscureBulkJobAuthorization(cause error) error {
	return notFound(cause)
}

func (b *httpBackend) listWallet(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	userID, err := publicUserID(call)
	if err != nil {
		return couponhttp.Result{}, err
	}
	limit, err := queryLimit(call)
	if err != nil {
		return couponhttp.Result{}, err
	}
	page, err := b.components.readRepo.ListWallet(ctx, readmodel.WalletQuery{
		UserID: userID, Status: readmodel.WalletStatus(call.Query.Get("status")),
		Cursor: call.Query.Get("cursor"), Limit: limit,
	})
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	items := make([]couponhttp.WalletCoupon, 0, len(page.Items))
	scopes := make([]readmodel.NoticeScope, 0, len(page.Items))
	var asOf time.Time
	for _, item := range page.Items {
		items = append(items, couponhttp.WalletCoupon{
			UserCouponID: item.UserCouponID, CampaignID: item.CampaignID, DisplayName: item.DisplayName,
			Benefit: readBenefit(item.Benefit), Status: string(item.Status),
			UsableFrom: formatTime(item.UsableFrom), ExpiresAt: formatTime(item.ExpiresAt),
		})
		scopes = append(scopes, readmodel.NoticeScope{Type: "campaign", Ref: item.CampaignID})
		asOf = later(asOf, item.UpdatedAt)
	}
	notices, noticeAsOf, err := b.activeNotices(ctx, scopes, b.currentTime())
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	asOf = later(asOf, noticeAsOf)
	return couponhttp.Result{
		Data: couponhttp.WalletCouponListData{Items: items, NextCursor: page.NextCursor, ActiveNotices: notices},
		AsOf: formatTime(asOf),
	}, nil
}

func (b *httpBackend) getCouponDetail(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	userID, err := publicUserID(call)
	if err != nil {
		return couponhttp.Result{}, err
	}
	result, err := b.components.readRepo.GetCouponDetail(ctx, userID, call.Path["userCouponId"])
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	document := result.Document
	data := couponhttp.CouponDetailData{
		UserCouponID: document.UserCouponID, CampaignID: document.CampaignID,
		DisplayName: document.DisplayName, Benefit: readBenefit(document.Benefit), Status: string(document.Status),
		UsableFrom: formatTime(document.UsableFrom), ExpiresAt: formatTime(document.ExpiresAt),
		PolicyVersion: int64(document.PolicyVersion), Applicability: readApplicability(document.Applicability),
		IssuerAndFunding: readFunding(document.IssuerAndFunding),
	}
	evaluatedAt := b.currentTime()
	notices, noticeAsOf, err := b.activeNotices(ctx, []readmodel.NoticeScope{{Type: "campaign", Ref: document.CampaignID}}, evaluatedAt)
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	if len(notices) > 0 {
		data.ActiveNotice = &notices[0]
	}
	return couponhttp.Result{Data: data, AsOf: formatTime(later(result.UpdatedAt, noticeAsOf))}, nil
}

func (b *httpBackend) activeNotices(ctx context.Context, scopes []readmodel.NoticeScope, asOf time.Time) ([]couponhttp.ReadOnlyNotice, time.Time, error) {
	if len(scopes) == 0 {
		return []couponhttp.ReadOnlyNotice{}, time.Time{}, nil
	}
	items, err := b.components.readRepo.ListActiveNotices(ctx, readmodel.NoticeQuery{Scopes: scopes, AsOf: asOf, Limit: 10})
	if err != nil {
		return nil, time.Time{}, err
	}
	result := make([]couponhttp.ReadOnlyNotice, 0, len(items))
	var projectionAsOf time.Time
	for _, item := range items {
		result = append(result, couponhttp.ReadOnlyNotice{
			ControlID: item.ControlID, Message: item.Message, EffectiveFrom: formatTime(item.EffectiveFrom),
		})
		projectionAsOf = later(projectionAsOf, item.UpdatedAt)
	}
	return result, projectionAsOf, nil
}

func (b *httpBackend) listRecoveries(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	limit, err := queryLimit(call)
	if err != nil {
		return couponhttp.Result{}, err
	}
	page, err := b.components.readRepo.ListFailures(ctx, readmodel.FailureQuery{
		Kind: "recovery", Status: call.Query.Get("status"),
		OriginalOperationType: domainRecoveryOperation(call.Query.Get("originalOperationType")),
		Cursor:                call.Query.Get("cursor"), Limit: limit,
	})
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	items := make([]couponhttp.RecoveryItem, 0, len(page.Items))
	var asOf time.Time
	for _, item := range page.Items {
		items = append(items, recoveryReadItem(item))
		asOf = later(asOf, item.UpdatedAt)
	}
	return couponhttp.Result{
		Data: couponhttp.RecoveryListData{Items: items, NextCursor: page.NextCursor}, AsOf: formatTime(asOf),
	}, nil
}

func (b *httpBackend) campaignPerformance(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	from, err := queryTime(call, "from")
	if err != nil {
		return couponhttp.Result{}, err
	}
	to, err := queryTime(call, "to")
	if err != nil {
		return couponhttp.Result{}, err
	}
	if _, err := b.components.campaignRepo.Get(ctx, call.Path["campaignId"]); err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	performance, err := b.components.readRepo.CampaignPerformance(ctx, readmodel.PerformanceQuery{
		CampaignID: call.Path["campaignId"], From: from, To: to,
	})
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	if performance.AsOf.IsZero() {
		return couponhttp.Result{}, dependencyUnavailable(errors.New("campaign performance projection is unavailable"))
	}
	return couponhttp.Result{
		Data: campaignPerformanceData(performance), AsOf: formatTime(performance.AsOf),
	}, nil
}

func (b *httpBackend) incidentStatus(ctx context.Context) (couponhttp.Result, error) {
	status, err := b.components.readRepo.GetIncidentStatus(ctx)
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	now := b.currentTime()
	if b.resources.DB == nil {
		return couponhttp.Result{}, dependencyUnavailable(errors.New("postgres resource is not configured"))
	}
	if err := b.resources.DB.Ping(ctx); err != nil {
		return couponhttp.Result{}, dependencyUnavailable(err)
	}
	postgres := couponhttp.SignalStatus{Status: "normal", AsOf: formatTime(now)}
	redis := couponhttp.SignalStatus{Status: "unavailable", AsOf: formatTime(now)}
	if b.resources.Redis != nil {
		if err := b.resources.Redis.Ping(ctx).Err(); err == nil {
			redis.Status = "normal"
		}
	}
	data := couponhttp.IncidentStatusData{
		Issuance:   businessSignal(status, readmodel.SignalIssuance, now),
		Redemption: businessSignal(status, readmodel.SignalRedemption, now),
		Recovery:   businessSignal(status, readmodel.SignalRecovery, now),
		Postgres:   postgres,
		Redis:      redis,
		MQ:         couponhttp.SignalStatus{Status: "unavailable", AsOf: formatTime(now)},
		Workers:    couponhttp.SignalStatus{Status: "unavailable", AsOf: formatTime(now)},
	}
	asOf := status.AsOf
	if asOf.IsZero() {
		asOf = now
	}
	return couponhttp.Result{Data: data, AsOf: formatTime(asOf)}, nil
}

func (b *httpBackend) listCostAttributions(ctx context.Context, call couponhttp.Call) (couponhttp.Result, error) {
	limit, err := queryLimit(call)
	if err != nil {
		return couponhttp.Result{}, err
	}
	from, err := queryTime(call, "from")
	if err != nil {
		return couponhttp.Result{}, err
	}
	to, err := queryTime(call, "to")
	if err != nil {
		return couponhttp.Result{}, err
	}
	orderID := call.Query.Get("orderRef")
	page, err := b.components.readRepo.ListCostAttributions(ctx, readmodel.CostAttributionQuery{
		OrderID: orderID, CampaignID: call.Query.Get("campaignId"), From: from, To: to,
		Cursor: call.Query.Get("cursor"), Limit: limit,
	})
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	items := make([]couponhttp.CostAttributionItem, 0, len(page.Items))
	var asOf time.Time
	for _, item := range page.Items {
		kind := string(item.Kind)
		if item.Kind == readmodel.CostAttributionReclaimed {
			kind = "reclaimed_adjustment"
		}
		items = append(items, couponhttp.CostAttributionItem{
			AttributionID: item.AttributionID.String(), OrderRef: readExternal(item.OrderRef),
			RedemptionID: item.RedemptionID, CampaignID: item.CampaignID, Kind: kind,
			Discount: readMoney(item.Discount), Shares: readCostShares(item.Shares), OccurredAt: formatTime(item.OccurredAt),
		})
		asOf = later(asOf, item.OccurredAt)
	}
	return couponhttp.Result{
		Data: couponhttp.CostAttributionListData{Items: items, NextCursor: page.NextCursor}, AsOf: formatTime(asOf),
	}, nil
}

func (b *httpBackend) currentCampaignResult(ctx context.Context, campaignID string) (couponhttp.Result, error) {
	data, err := b.campaignResponse(ctx, campaignID)
	if err != nil {
		return couponhttp.Result{}, transportError(err)
	}
	return couponhttp.Result{Data: data}, nil
}

func (b *httpBackend) campaignResponse(ctx context.Context, campaignID string) (couponhttp.CampaignData, error) {
	current, err := b.components.campaignRepo.Get(ctx, campaignID)
	if err != nil {
		return couponhttp.CampaignData{}, err
	}
	return couponhttp.CampaignData{
		CampaignID: current.ID, Status: string(current.Status),
		PolicyVersion: current.CurrentPolicyVersion, Version: current.Version,
	}, nil
}

func (b *httpBackend) campaignMetadata(call couponhttp.Call, commandID, businessKey string) campaignapp.CommandMetadata {
	now := b.currentTime()
	return campaignapp.CommandMetadata{
		CommandID: commandID, BusinessKey: businessKey, CorrelationID: call.Headers.RequestID,
		CausationID: commandID, TraceID: traceID(call.Headers.Traceparent), ApprovalRef: call.Headers.ApprovalRef,
		OccurredAt: now, LeaseUntil: now.Add(b.policy.CommandLease), ExpiresAt: now.Add(b.policy.IdempotencyTTL),
	}
}

func (b *httpBackend) issuanceMetadata(call couponhttp.Call, commandID, businessKey string) issuanceapp.CommandMetadata {
	now := b.currentTime()
	return issuanceapp.CommandMetadata{
		CommandID: commandID, BusinessKey: businessKey, CorrelationID: call.Headers.RequestID,
		CausationID: commandID, TraceID: traceID(call.Headers.Traceparent), ApprovalRef: call.Headers.ApprovalRef,
		OccurredAt: now, LeaseUntil: now.Add(b.policy.CommandLease), ExpiresAt: now.Add(b.policy.IdempotencyTTL),
	}
}

func (b *httpBackend) redemptionMetadata(call couponhttp.Call, businessKey string) redemptionapp.Metadata {
	now := b.currentTime()
	return redemptionapp.Metadata{
		IdempotencyKey: call.Headers.IdempotencyKey, BusinessKey: businessKey,
		CorrelationID: call.Headers.RequestID, CausationID: commandIDForAPI(call.OperationID),
		TraceID:     traceID(call.Headers.Traceparent),
		RequestedAt: now, LeaseUntil: now.Add(b.policy.CommandLease), ExpiresAt: now.Add(b.policy.IdempotencyTTL),
	}
}

func (b *httpBackend) operationsMetadata(call couponhttp.Call, businessKey string) operations.Metadata {
	return b.operationsMetadataAt(call, businessKey, b.currentTime())
}

func (b *httpBackend) operationsMetadataAt(call couponhttp.Call, businessKey string, now time.Time) operations.Metadata {
	return operations.Metadata{
		IdempotencyKey: call.Headers.IdempotencyKey, BusinessKey: businessKey,
		CorrelationID: call.Headers.RequestID, CausationID: commandIDForAPI(call.OperationID), TraceID: traceID(call.Headers.Traceparent),
		RequestedAt: now, LeaseUntil: now.Add(b.policy.CommandLease), ExpiresAt: now.Add(b.policy.IdempotencyTTL),
	}
}

func (b *httpBackend) currentTime() time.Time {
	if b.now == nil {
		return time.Now().UTC()
	}
	return b.now().UTC()
}

func issueAcceptedResult(issueRequestID string) couponhttp.Result {
	return couponhttp.Result{
		Data: couponhttp.IssueAcceptedData{
			IssueRequestID: issueRequestID, Status: "pending", StatusPath: issueStatusPath,
		},
		Location: issueStatusPath, RetryAfterSeconds: asyncRetrySeconds,
	}
}

func validationData(value domainredemption.Redemption, snapshot couponhttp.SnapshotRef) couponhttp.CouponValidationData {
	shares := make([]couponhttp.CostShare, 0, len(value.CostShares))
	for _, item := range value.CostShares {
		share := couponhttp.CostShare{BearerType: item.BearerType, Amount: transportMoney(item.Amount)}
		if item.BearerRef != nil {
			ref := transportExternal(*item.BearerRef)
			share.BearerRef = &ref
		}
		shares = append(shares, share)
	}
	return couponhttp.CouponValidationData{
		RedemptionID: value.ID, Eligible: value.Status == domainredemption.StatusEvaluated,
		ReasonCode: value.ReasonCode, Discount: transportMoney(value.Discount),
		FinalOrderAmount: transportMoney(value.FinalOrderAmount), CostShares: shares,
		PolicyVersion: int64(value.PolicyVersion), OrderSnapshotRef: snapshot, Version: value.Version,
	}
}

func redemptionData(value domainredemption.Redemption) couponhttp.RedemptionData {
	result := couponhttp.RedemptionData{
		RedemptionID: value.ID, Status: string(value.Status), Version: value.Version,
		Discount: transportMoney(value.Discount),
	}
	if value.ReservedUntil != nil {
		result.ReservedUntil = formatTime(*value.ReservedUntil)
	}
	if value.Status == domainredemption.StatusConfirmed || value.Status == domainredemption.StatusReleased || value.Status == domainredemption.StatusReclaimed {
		ref := transportExternal(value.ResultRef)
		result.ResultRef = &ref
	}
	return result
}

func redemptionOrder(value couponhttp.OrderCandidateSnapshot) (redemptionapp.OrderSnapshot, error) {
	snapshot, err := domainSnapshot(value.SnapshotRef)
	if err != nil {
		return redemptionapp.OrderSnapshot{}, err
	}
	items := make([]redemptionapp.OrderItem, 0, len(value.Items))
	for _, item := range value.Items {
		converted := redemptionapp.OrderItem{
			ProductRef: domainExternal(item.ProductRef), SellerRef: domainExternal(item.SellerRef),
			Quantity: int64(*item.Quantity), UnitPrice: domainMoney(item.UnitPrice),
		}
		if item.DropRef != nil {
			ref := domainExternal(*item.DropRef)
			converted.DropRef = &ref
		}
		if item.CategoryRef != nil {
			ref := domainExternal(*item.CategoryRef)
			converted.CategoryRef = &ref
		}
		items = append(items, converted)
	}
	return redemptionapp.OrderSnapshot{
		Ref: snapshot, OrderID: value.OrderID, UserID: value.UserID,
		Items: items, ShippingFee: domainMoney(value.ShippingFee),
	}, nil
}

func redemptionTransition(redemptionID string, value *couponhttp.RedemptionTransitionRequest) (redemptionapp.TransitionInput, error) {
	result := redemptionapp.TransitionInput{
		RedemptionID: redemptionID, ExpectedVersion: *value.ExpectedVersion,
		ResultRef: domainExternal(value.ResultRef), ReasonCode: value.ReasonCode,
	}
	if value.ResultSnapshot != nil {
		snapshot, err := domainSnapshot(*value.ResultSnapshot)
		if err != nil {
			return redemptionapp.TransitionInput{}, err
		}
		result.ResultSnapshot = &snapshot
	}
	return result, nil
}

func bulkJobData(job bulk.Job) couponhttp.BulkJobData {
	status := string(job.Status)
	if job.Status == bulk.StatusCompletedWithFailures {
		status = string(bulk.StatusCompleted)
	}
	return couponhttp.BulkJobData{
		BulkJobID: job.ID, CampaignID: job.CampaignID, Status: status,
		Counts: couponhttp.PerformanceCounts{
			Requested: job.TargetCount, Issued: job.SucceededCount,
			Rejected: job.RejectedCount, FailedFinal: job.FailedCount,
		},
		EvaluationAsOf: formatTime(job.EvaluationAsOf),
	}
}

func campaignPerformanceData(value readmodel.CampaignPerformance) couponhttp.CampaignPerformanceData {
	result := couponhttp.CampaignPerformanceData{
		CampaignID: value.CampaignID, Scope: "operations", AsOf: formatTime(value.AsOf),
		Counts: couponhttp.PerformanceCounts{
			Requested: value.Counts.Requested, Issued: value.Counts.Issued,
			Rejected: value.Counts.Rejected, FailedFinal: value.Counts.FailedFinal,
			Reserved: value.Counts.Reserved, Confirmed: value.Counts.Confirmed,
			Released: value.Counts.Released, Reclaimed: value.Counts.Reclaimed,
		},
	}
	if value.ConfirmedDiscount != nil {
		confirmed := readMoney(*value.ConfirmedDiscount)
		result.ConfirmedDiscount = &confirmed
	}
	if value.ReclaimedDiscount != nil {
		reclaimed := readMoney(*value.ReclaimedDiscount)
		result.ReclaimedDiscount = &reclaimed
	}
	return result
}

func operationalControlData(control domainoperations.Control, sourceRef couponhttp.ExternalRef) couponhttp.OperationalControlData {
	scope := domainoperations.Scope{}
	if len(control.Scopes) > 0 {
		scope = control.Scopes[0]
	}
	if sourceRef.ID == "" {
		sourceRef = couponhttp.ExternalRef{Context: string(scope.Type), Type: string(scope.Type), ID: scope.Ref}
	}
	return couponhttp.OperationalControlData{
		ControlID:     control.ID,
		Scope:         couponhttp.OperationalScope{Type: string(scope.Type), Ref: sourceRef},
		BlockIssuance: control.BlockIssuance, BlockRedemption: control.BlockRedemption,
		EffectiveFrom: formatTime(control.EffectiveFrom), Active: control.Active, Version: control.Version,
	}
}

func readOnlyNoticeData(control domainoperations.Control) couponhttp.ReadOnlyNoticeData {
	return couponhttp.ReadOnlyNoticeData{
		ControlID: control.ID, Message: control.Notice.Message,
		EffectiveFrom: formatTime(control.Notice.EffectiveFrom), Active: control.Notice.Active, Version: control.Version,
	}
}

func recoveryReadItem(item readmodel.Failure) couponhttp.RecoveryItem {
	result := couponhttp.RecoveryItem{
		RecoveryID: item.FailureID, Status: item.Status,
		OriginalOperationType: publicRecoveryOperation(item.OriginalOperation),
		OriginalPayloadRef:    couponhttp.ExternalRef{Context: "coupon", Type: "replay_payload", ID: item.SourceRef},
		BusinessKeyRef:        businessKeyRef(item.BusinessKey), AttemptID: item.CurrentAttemptID,
		AttemptCount: int64(item.AttemptCount), ResultKind: item.ResultKind,
		FailureCode: item.FailureCode, UpdatedAt: formatTime(item.UpdatedAt),
	}
	if item.NextAttemptAt != nil {
		result.NextAttemptAt = formatTime(*item.NextAttemptAt)
	}
	if item.ResultRef != "" {
		ref := couponhttp.ExternalRef{Context: "coupon", Type: "recovery_result", ID: item.ResultRef}
		result.ResultRef = &ref
	}
	return result
}

func recoveryAggregateItem(item recovery.Recovery) couponhttp.RecoveryItem {
	result := couponhttp.RecoveryItem{
		RecoveryID: item.ID, Status: string(item.Status),
		OriginalOperationType: publicRecoveryOperation(string(item.OriginalOperationType)),
		OriginalPayloadRef:    couponhttp.ExternalRef{Context: "coupon", Type: "replay_payload", ID: item.OriginalPayloadRef},
		BusinessKeyRef:        businessKeyRef(item.BusinessKey), AttemptID: item.CurrentAttemptID,
		AttemptCount: int64(item.AttemptCount), ResultKind: string(item.ResultKind),
		FailureCode: item.FailureCode, UpdatedAt: formatTime(item.UpdatedAt),
	}
	if item.NextAttemptAt != nil {
		result.NextAttemptAt = formatTime(*item.NextAttemptAt)
	}
	if item.ResultRef != "" {
		ref := couponhttp.ExternalRef{Context: "coupon", Type: "recovery_result", ID: item.ResultRef}
		result.ResultRef = &ref
	}
	return result
}

func readCostShares(values []readmodel.CostShare) []couponhttp.CostShare {
	result := make([]couponhttp.CostShare, 0, len(values))
	for _, item := range values {
		share := couponhttp.CostShare{BearerType: item.BearerType, Amount: readMoney(item.Amount)}
		if item.BearerRef != nil {
			ref := readExternal(*item.BearerRef)
			share.BearerRef = &ref
		}
		result = append(result, share)
	}
	return result
}

func businessSignal(status readmodel.IncidentStatus, name readmodel.SignalName, now time.Time) couponhttp.SignalStatus {
	signal, ok := status.Signals[name]
	if !ok || signal.ObservedAt.IsZero() {
		return couponhttp.SignalStatus{Status: "unavailable", AsOf: formatTime(now)}
	}
	lag := int64(now.Sub(signal.ObservedAt).Seconds())
	if lag < 0 {
		lag = 0
	}
	return couponhttp.SignalStatus{Status: signal.Status, AsOf: formatTime(signal.ObservedAt), LagSeconds: &lag}
}

func commandBusinessKey(call couponhttp.Call, scope ...string) string {
	actor := "unknown"
	switch call.Principal.Type {
	case principal.TypeUser:
		actor = "user:" + call.Principal.UserID
	case principal.TypeService:
		actor = "service:" + call.Principal.ServiceID
	}
	parts := []string{call.OperationID, actor}
	for _, value := range scope {
		if value != "" {
			parts = append(parts, value)
		}
	}
	parts = append(parts, call.Headers.IdempotencyKey)
	return strings.Join(parts, "|")
}

func commandIDForAPI(operationID string) string {
	return map[string]string{
		"API.A.19-01": "CMD.A.19-05", "API.A.19-02": "CMD.A.19-06",
		"API.A.19-05": "CMD.A.19-09", "API.A.19-06": "CMD.A.19-10",
		"API.A.19-07": "CMD.A.19-11", "API.A.19-08": "CMD.A.19-12",
		"API.A.19-09": "CMD.A.19-15", "API.A.19-10": "CMD.A.19-01",
		"API.A.19-11": "CMD.A.19-02", "API.A.19-12": "CMD.A.19-03",
		"API.A.19-13": "CMD.A.19-04", "API.A.19-14": "CMD.A.19-08",
		"API.A.19-17": "CMD.A.19-20", "API.A.19-18": "CMD.A.19-31",
		"API.A.19-20": "CMD.A.19-21", "API.A.19-21": "CMD.A.19-25",
		"API.A.19-23": "CMD.A.19-13",
	}[operationID]
}

func publicUserID(call couponhttp.Call) (string, error) {
	if strings.TrimSpace(call.Principal.UserID) == "" {
		return "", httpcontract.Internal(errors.New("public user principal is missing"))
	}
	return call.Principal.UserID, nil
}

func queryLimit(call couponhttp.Call) (int, error) {
	raw := call.Query.Get("limit")
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > readmodel.MaxLimit {
		return 0, httpcontract.InputInvalid("limit", "out_of_range")
	}
	return value, nil
}

func queryTime(call couponhttp.Call, name string) (*time.Time, error) {
	raw := call.Query.Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := parseTime(raw)
	if err != nil {
		return nil, httpcontract.InputInvalid(name, "invalid_date_time")
	}
	return &value, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func traceID(traceparent string) string {
	parts := strings.Split(traceparent, "-")
	if len(parts) == 4 && len(parts[1]) == 32 {
		return parts[1]
	}
	return ""
}

func businessKeyRef(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "business-key:" + hex.EncodeToString(digest[:])
}

func domainRecoveryOperation(value string) string {
	switch value {
	case "commit":
		return "confirm"
	case "revoke":
		return "reclaim"
	default:
		return value
	}
}

func publicRecoveryOperation(value string) string {
	switch value {
	case "confirm":
		return "commit"
	case "reclaim":
		return "revoke"
	default:
		return value
	}
}

func later(current, candidate time.Time) time.Time {
	if candidate.After(current) {
		return candidate
	}
	return current
}

func backendInvariant() (couponhttp.Result, error) {
	return couponhttp.Result{}, httpcontract.Internal(errors.New("coupon HTTP operation is not wired"))
}
