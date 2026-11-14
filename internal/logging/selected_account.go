package logging

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

const selectedAccountKey = "__selected_account__"

var accountEmailPattern = regexp.MustCompile(`[\w.%+]+@[\w-]+(\.[\w-]+)*\.[a-z]+`)

// SetSelectedAccount records which account served the request.
func SetSelectedAccount(c *gin.Context, authID string) {
	if c != nil && authID != "" {
		c.Set(selectedAccountKey, accountLabel(authID))
	}
}

func selectedAccount(c *gin.Context) string {
	return c.GetString(selectedAccountKey)
}

// accountLabel keeps the email from a login file's name, because that is how
// people know their accounts. API key entries have no email, so their ID stays.
func accountLabel(authID string) string {
	trimmed := strings.TrimSuffix(authID, ".json")
	if email := accountEmailPattern.FindString(trimmed); email != "" {
		return email
	}
	return trimmed
}
