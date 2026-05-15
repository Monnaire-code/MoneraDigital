package pool

import (
	"errors"
	"time"
)

type Address struct {
	ID                  int
	NetworkFamily       string
	Address             string
	SafeheronAccountKey string
	CustomerRefID       string
	AddressGroupKey     string
	DerivePath          string
	AccountTag          string
	HiddenOnUI          bool
	AutoFuel            bool
	Status              string
	AssignedUserID      *int
	AssignedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const (
	StatusAvailable = "AVAILABLE"
	StatusAssigned  = "ASSIGNED"
	StatusError     = "ERROR"
)

var ErrPoolEmpty = errors.New("address pool exhausted")
