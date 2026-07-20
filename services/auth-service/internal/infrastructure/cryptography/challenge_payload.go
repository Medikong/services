package cryptography

import (
	applicationchallenge "github.com/Medikong/services/services/auth-service/internal/application/challenge"
)

type ChallengePayloadOpener struct {
	keys Keys
}

func NewChallengePayloadOpener(keys Keys) *ChallengePayloadOpener {
	return &ChallengePayloadOpener{keys: keys}
}

func (o *ChallengePayloadOpener) OpenDelivery(ciphertext []byte) (applicationchallenge.DeliverySecret, error) {
	var payload struct {
		Code        string `json:"code"`
		Destination string `json:"destination"`
	}
	if err := o.keys.Open(ciphertext, &payload); err != nil {
		return applicationchallenge.DeliverySecret{}, err
	}
	return applicationchallenge.DeliverySecret{Code: payload.Code, Destination: payload.Destination}, nil
}

var _ applicationchallenge.PayloadOpener = (*ChallengePayloadOpener)(nil)
