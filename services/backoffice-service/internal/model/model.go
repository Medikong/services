package model

type PrepareDropInput struct {
	ProductID     string            `json:"productId"`
	ProductName   string            `json:"productName"`
	DropID        string            `json:"dropId"`
	SaleStartsAt  string            `json:"saleStartsAt"`
	StockQuantity int               `json:"stockQuantity"`
	CouponPolicy  CouponPolicyInput `json:"couponPolicy"`
}

type CouponPolicyInput struct {
	PolicyID      string `json:"policyId"`
	Name          string `json:"name"`
	TotalQuantity int    `json:"totalQuantity"`
}

type Readiness struct {
	DropID string           `json:"dropId"`
	Ready  bool             `json:"ready"`
	Checks map[string]Check `json:"checks"`
}

type Check struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
}
