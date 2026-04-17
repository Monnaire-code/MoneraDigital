// internal/dto/address.go
package dto

import "time"

// AddAddressRequest DTO for adding a withdrawal address
type AddAddressRequest struct {
	Address     string `json:"address" binding:"required,min=20,max=100"`
	AddressType string `json:"addressType" binding:"required,oneof=BTC ETH USDC USDT"`
	Label       string `json:"label" binding:"required,min:1,max:50"`
}

// WithdrawalAddressResponse DTO for withdrawal address response
type WithdrawalAddressResponse struct {
	ID          int        `json:"id"`
	UserID      int        `json:"userId"`
	Address     string     `json:"walletAddress"`
	Type        string     `json:"chainType"`
	Label       string     `json:"addressAlias"`
	IsVerified  bool       `json:"verified"`
	IsDeleted   bool       `json:"isDeleted"`
	CreatedAt   time.Time  `json:"createdAt"`
	VerifiedAt  *time.Time `json:"verifiedAt,omitempty"`
	FrozenUntil *time.Time `json:"frozenUntil,omitempty"`
}

// WithdrawalAddressesListResponse DTO for list of withdrawal addresses
type WithdrawalAddressesListResponse struct {
	Addresses []WithdrawalAddressResponse `json:"addresses"`
	Total     int                         `json:"total"`
	Count     int                         `json:"count"`
}

// VerifyAddressRequest DTO for address verification
type VerifyAddressRequest struct {
	AddressID int    `json:"addressId" binding:"required,gt=0"`
	Token     string `json:"token" binding:"required"`
}

// SetPrimaryAddressRequest DTO for setting primary address
type SetPrimaryAddressRequest struct {
	AddressID int `json:"addressId" binding:"required,gt=0"`
}

// DeactivateAddressRequest DTO for deactivating address
type DeactivateAddressRequest struct {
	AddressID int `json:"addressId" binding:"required,gt=0"`
}
