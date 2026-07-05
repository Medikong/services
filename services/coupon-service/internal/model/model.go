package model

type Policy struct {
	PolicyID      string `json:"policyId"`
	DropID        string `json:"dropId"`
	Name          string `json:"name"`
	TotalQuantity int    `json:"totalQuantity"`
	IssuedCount   int    `json:"issuedCount"`
	Status        string `json:"status"`
}

type Coupon struct {
	CouponID string `json:"couponId"`
	PolicyID string `json:"policyId"`
	DropID   string `json:"dropId"`
	UserID   string `json:"userId"`
	Status   string `json:"status"`
}

type IssueResult struct {
	Result string `json:"result"`
	Coupon Coupon `json:"coupon"`
}
