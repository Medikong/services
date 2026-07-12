package projection

import "fmt"

const (
	RMUserCouponWallet      = "RM.A.19-01"
	RMCouponDetails         = "RM.A.19-02"
	RMCouponPerformance     = "RM.A.19-04"
	RMCouponFailures        = "RM.A.19-05"
	RMUserCouponTimeline    = "RM.A.19-06"
	RMCouponIncidentStatus  = "RM.A.19-07"
	RMCouponCostAttribution = "RM.A.19-08"
	RMCouponReadOnlyNotice  = "RM.A.19-09"
)

type EventCoverage struct {
	EventDocumentID string
	ReadModels      []string
	Reason          string
}

var coverageByEvent = func() map[string]EventCoverage {
	result := make(map[string]EventCoverage, 41)
	for index := 1; index <= 41; index++ {
		id := fmt.Sprintf("EVT.A.19-%02d", index)
		result[id] = EventCoverage{EventDocumentID: id, Reason: "도메인 쓰기 모델 전용 Event"}
	}
	set := func(id string, models ...string) {
		result[id] = EventCoverage{EventDocumentID: id, ReadModels: models}
	}
	set("EVT.A.19-07", RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-08", RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-09", RMUserCouponWallet, RMCouponDetails, RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-10", RMCouponFailures, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-11", RMCouponPerformance, RMCouponFailures, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-18", RMCouponFailures, RMCouponIncidentStatus)
	for index := 19; index <= 24; index++ {
		set(fmt.Sprintf("EVT.A.19-%02d", index), RMUserCouponTimeline, RMCouponIncidentStatus)
	}
	set("EVT.A.19-21", RMUserCouponWallet, RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-22", RMUserCouponWallet, RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-23", RMUserCouponWallet, RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-24", RMUserCouponWallet, RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-25", RMCouponIncidentStatus)
	set("EVT.A.19-26", RMCouponFailures, RMCouponIncidentStatus)
	set("EVT.A.19-27", RMCouponFailures, RMCouponIncidentStatus)
	set("EVT.A.19-28", RMCouponCostAttribution)
	set("EVT.A.19-29", RMUserCouponTimeline, RMCouponIncidentStatus)
	set("EVT.A.19-30", RMCouponFailures, RMCouponIncidentStatus)
	set("EVT.A.19-31", RMUserCouponWallet, RMUserCouponTimeline, RMCouponIncidentStatus)
	for index := 32; index <= 36; index++ {
		set(fmt.Sprintf("EVT.A.19-%02d", index), RMCouponIncidentStatus)
	}
	set("EVT.A.19-37", RMCouponFailures, RMCouponIncidentStatus)
	set("EVT.A.19-38", RMCouponReadOnlyNotice)
	set("EVT.A.19-39", RMCouponFailures, RMCouponIncidentStatus)
	set("EVT.A.19-40", RMCouponFailures, RMCouponIncidentStatus)
	set("EVT.A.19-41", RMCouponFailures, RMCouponIncidentStatus)
	return result
}()

// Coverage returns all 41 authoritative Event IDs. An empty ReadModels slice is
// an intentional no-op, not an unknown Event. RM.A.19-03 is deliberately never
// returned while HOTSPOT.A.19-07 remains unresolved.
func Coverage() []EventCoverage {
	result := make([]EventCoverage, 0, 41)
	for index := 1; index <= 41; index++ {
		entry := coverageByEvent[fmt.Sprintf("EVT.A.19-%02d", index)]
		entry.ReadModels = append([]string(nil), entry.ReadModels...)
		result = append(result, entry)
	}
	return result
}

func coverage(eventDocumentID string) (EventCoverage, bool) {
	value, ok := coverageByEvent[eventDocumentID]
	return value, ok
}
