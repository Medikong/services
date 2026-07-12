package policy

type Mode string

const (
	ModeGuard    Mode = "guard"
	ModeEvent    Mode = "event"
	ModeWorker   Mode = "worker"
	ModeExternal Mode = "external"
)

type Route struct {
	SourceEventID string
	CommandID     string
	AggregateType string
	TargetIDField string
	Enabled       bool
	UnresolvedBy  string
}

type Definition struct {
	ID     string
	Name   string
	Mode   Mode
	Routes []Route
}

func Definitions() []Definition {
	return []Definition{
		{ID: "POLICY.A.19-01", Name: "발급 주체·비용 부담 명시", Mode: ModeGuard},
		{ID: "POLICY.A.19-02", Name: "판매자 소유 범위 제한", Mode: ModeGuard},
		{ID: "POLICY.A.19-03", Name: "쿠폰 승인 게이트", Mode: ModeGuard},
		{ID: "POLICY.A.19-04", Name: "발급 자격·수량 제한", Mode: ModeGuard},
		{ID: "POLICY.A.19-05", Name: "코드 등록 유효성", Mode: ModeGuard},
		{ID: "POLICY.A.19-06", Name: "주문 스냅샷 검증", Mode: ModeGuard},
		{ID: "POLICY.A.19-07", Name: "정책 버전 적용", Mode: ModeGuard},
		{ID: "POLICY.A.19-08", Name: "운영 중지 우선", Mode: ModeGuard},
		{ID: "POLICY.A.19-09", Name: "발급 요청 후속 처리", Mode: ModeWorker, Routes: []Route{
			{SourceEventID: "EVT.A.19-36", CommandID: "CMD.A.19-07", AggregateType: "UserCoupon", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-10", Name: "코드 등록 보상 처리", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-09", CommandID: "CMD.A.19-16", AggregateType: "CouponCodeBatch", TargetIDField: "issue_request_id", Enabled: true},
			{SourceEventID: "EVT.A.19-11", CommandID: "CMD.A.19-17", AggregateType: "CouponCodeBatch", TargetIDField: "issue_request_id", Enabled: true},
			{SourceEventID: "EVT.A.19-08", CommandID: "CMD.A.19-17", AggregateType: "CouponCodeBatch", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-11", Name: "발급 요청 생성", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-12", CommandID: "CMD.A.19-13", AggregateType: "CouponIssueRequest", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-12", Name: "대량 발급 결과 반영", Mode: ModeEvent, Routes: []Route{
			// Terminal issuance events carry issue_request_id. The dispatcher
			// verifies source_type=bulk and resolves bulk_job_id from source_ref.
			{SourceEventID: "EVT.A.19-09", CommandID: "CMD.A.19-18", AggregateType: "BulkCouponIssueJob", TargetIDField: "issue_request_id", Enabled: true},
			{SourceEventID: "EVT.A.19-08", CommandID: "CMD.A.19-18", AggregateType: "BulkCouponIssueJob", TargetIDField: "issue_request_id", Enabled: true},
			{SourceEventID: "EVT.A.19-11", CommandID: "CMD.A.19-18", AggregateType: "BulkCouponIssueJob", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-13", Name: "발급 수량 예약", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-07", CommandID: "CMD.A.19-26", AggregateType: "CouponCampaign", TargetIDField: "campaign_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-14", Name: "발급 실패 재처리 결정", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-10", CommandID: "CMD.A.19-19", AggregateType: "CouponIssueRequest", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-15", Name: "발급 성공 요청 완료", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-09", CommandID: "CMD.A.19-23", AggregateType: "CouponIssueRequest", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-16", Name: "시스템 자동 지급 요청 변환", Mode: ModeExternal, Routes: []Route{
			{CommandID: "CMD.A.19-13", AggregateType: "CouponIssueRequest", Enabled: false, UnresolvedBy: "HOTSPOT.A.19-09"},
		}},
		{ID: "POLICY.A.19-17", Name: "만료 시각 도달 처리", Mode: ModeWorker, Routes: []Route{
			{CommandID: "CMD.A.19-24", AggregateType: "UserCoupon", TargetIDField: "user_coupon_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-18", Name: "만료 쿠폰 예약 해제", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-31", CommandID: "CMD.A.19-12", AggregateType: "CouponRedemption", TargetIDField: "redemption_id", Enabled: false, UnresolvedBy: "HOTSPOT.A.19-03"},
		}},
		{ID: "POLICY.A.19-19", Name: "발급 수량 결과 반영", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-33", CommandID: "CMD.A.19-29", AggregateType: "CouponIssueRequest", TargetIDField: "issue_request_id", Enabled: true},
			{SourceEventID: "EVT.A.19-09", CommandID: "CMD.A.19-27", AggregateType: "CouponCampaign", TargetIDField: "campaign_id", Enabled: true},
			{SourceEventID: "EVT.A.19-11", CommandID: "CMD.A.19-28", AggregateType: "CouponCampaign", TargetIDField: "campaign_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-20", Name: "발급 처리 대기 전환", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-32", CommandID: "CMD.A.19-30", AggregateType: "CouponIssueRequest", TargetIDField: "issue_request_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-21", Name: "실패 원본 업무 재실행", Mode: ModeWorker, Routes: []Route{
			{SourceEventID: "EVT.A.19-39", CommandID: "CMD.A.19-32", AggregateType: "CouponRedemption", TargetIDField: "redemption_id", Enabled: true},
		}},
		{ID: "POLICY.A.19-22", Name: "재처리 결과 반영", Mode: ModeEvent, Routes: []Route{
			{SourceEventID: "EVT.A.19-41", CommandID: "CMD.A.19-33", AggregateType: "CouponEventRecovery", TargetIDField: "recovery_id", Enabled: true},
		}},
	}
}

func EventRoutes(eventID string) []struct {
	Policy Definition
	Route  Route
} {
	var matches []struct {
		Policy Definition
		Route  Route
	}
	for _, definition := range Definitions() {
		if definition.Mode != ModeEvent {
			continue
		}
		for _, route := range definition.Routes {
			if route.Enabled && route.SourceEventID == eventID {
				matches = append(matches, struct {
					Policy Definition
					Route  Route
				}{Policy: definition, Route: route})
			}
		}
	}
	return matches
}
