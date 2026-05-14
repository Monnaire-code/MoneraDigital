# Proposal: Fix Duplicate Address Error Handling and Internationalization

## Problem
When a user attempts to add a withdrawal address that already exists, the backend returns a raw SQL error message:
`ERROR: duplicate key value violates unique constraint "uk_user_address" (SQLSTATE 23505)`

This negatively affects user experience because:
1.  It is technical and not user-friendly.
2.  It is not localized (always in English).
3.  The frontend displays it as a generic error (or raw).

## Root Cause
1.  **Backend (Repository)**: The `CreateAddress` method in `internal/repository/postgres/address.go` checks for a specific PostgreSQL error string (`withdrawal_address_whitelist_user_id_wallet_address_key`) which does not match the actual constraint name (`uk_user_address`) or format returned by the database driver.
2.  **Backend (Handler)**: The `AddAddress` handler in `internal/handlers/handlers.go` returns `500 Internal Server Error` for all errors, instead of distinguishing conflicts.
3.  **Frontend**: The frontend displays the raw error message returned by the backend without attempting to map it to a localized string.

## Proposed Solution

### Backend
1.  **Update Repository**: Improve duplicate key detection in `internal/repository/postgres/address.go` by checking for the PostgreSQL error code `23505` (unique_violation) using `lib/pq`, which is robust against constraint name changes.
2.  **Update Handler**: Modify `internal/handlers/handlers.go` to check if the error is `address already exists` (or `repository.ErrAlreadyExists` wrapped) and return `409 Conflict` status code.

### Frontend
1.  **Update Withdraw/Addresses Pages**: Update `src/pages/dashboard/Withdraw.tsx` and `src/pages/dashboard/Addresses.tsx` to handle `409 Conflict` response status.
2.  **Internationalization**: Add a new translation key `addresses.duplicateError` to `en.json` and `zh.json`.
3.  **Display**: Show the localized error message when a 409 status is received.

## Implementation Details

### Backend
**File:** `internal/repository/postgres/address.go`
```go
import "github.com/lib/pq"

// ...
if err != nil {
    if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
        return nil, repository.ErrAlreadyExists
    }
    // Fallback for string matching if needed, but code check is preferred
    return nil, err
}
```

**File:** `internal/handlers/handlers.go`
```go
if err != nil {
    if err.Error() == "address already exists" {
        c.JSON(http.StatusConflict, gin.H{"error": "Address already exists"})
        return
    }
    c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
    return
}
```

### Frontend
**Translation Keys:**
*   `en.json`: `"duplicateError": "This address has already been added."`
*   `zh.json`: `"duplicateError": "该地址已存在，请勿重复添加。"`

**Code:**
```typescript
if (res.status === 409) {
    toast.error(t("addresses.duplicateError"));
} else {
    toast.error(data.error || "Failed to add address");
}
```

## Testing Plan
1.  **Unit Test**: Create a test for the Handler to ensure it returns 409 when the service returns "address already exists".
2.  **Manual Verification**:
    *   Try to add the same address twice.
    *   Verify the second attempt shows the localized error message.

## Design Principles Checklist
- [x] **KISS**: Using standard error codes and i18n.
- [x] **High Cohesion**: DB specifics in Repo, HTTP specifics in Handler.
- [x] **Testing**: Adding test coverage.
- [x] **Isolation**: Changes scoped to address creation.
