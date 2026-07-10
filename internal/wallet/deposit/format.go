package deposit

import "fmt"

// formatUserID formats a user id for alert fields. Zero means "no user"
// (e.g. address unassigned) and is shown as "N/A" to avoid ops misreading "0".
func formatUserID(id int) string {
	if id == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%d", id)
}
