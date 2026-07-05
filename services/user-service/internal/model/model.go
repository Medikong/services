package model

type User struct {
	UserID      string `json:"userId"`
	RealName    string `json:"realName"`
	Nickname    string `json:"nickname"`
	ProfileIcon string `json:"profileIcon"`
	Status      string `json:"status"`
}

type ProfileUpdate struct {
	RealName    *string `json:"realName,omitempty"`
	Nickname    *string `json:"nickname,omitempty"`
	ProfileIcon *string `json:"profileIcon,omitempty"`
}
