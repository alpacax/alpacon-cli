package workspace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	paymentAPIProdURL    = "https://pay.alpacax.com"
	paymentAPIStagingURL = "https://pay.staging.alpacax.com"
	paymentWorkspacesURL = "/api/workspaces/workspaces/"
)

// GetPaymentAPIBaseURL determines the payment API base URL from the workspace URL.
// Dev regions use the staging payment API; all others use production.
func GetPaymentAPIBaseURL(workspaceURL string) (string, error) {
	parsed, err := url.Parse(workspaceURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse workspace URL: %w", err)
	}

	// Host format: {schema_name}.{region}.alpacon.io
	parts := strings.Split(parsed.Hostname(), ".")
	if len(parts) >= 4 {
		region := parts[1]
		if strings.Contains(region, "dev") {
			return paymentAPIStagingURL, nil
		}
	}

	return paymentAPIProdURL, nil
}

// GetWorkspaceID retrieves the workspace UUID from the payment API by matching schema_name.
func GetWorkspaceID(ac *client.AlpaconClient, paymentBaseURL, workspaceName string) (string, error) {
	listURL := paymentBaseURL + paymentWorkspacesURL
	body, err := ac.SendGetRequestToURL(listURL)
	if err != nil {
		return "", fmt.Errorf("failed to list workspaces from payment API: %w", err)
	}

	var response struct {
		Results []struct {
			ID         string `json:"id"`
			SchemaName string `json:"schema_name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to parse workspace list: %w", err)
	}

	for _, ws := range response.Results {
		if ws.SchemaName == workspaceName {
			return ws.ID, nil
		}
	}

	return "", fmt.Errorf("workspace %q not found in payment API", workspaceName)
}

// BillingPeriod represents the billing period for a workspace.
type BillingPeriod struct {
	Start     string `json:"start"`
	End       string `json:"end"`
	TotalDays int    `json:"total_days"`
}

// SubscriptionPeriod represents the subscription period.
type SubscriptionPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Subscription represents the subscription details.
type Subscription struct {
	ProductName  string             `json:"product_name"`
	PlanName     string             `json:"plan_name"`
	ProductCode  string             `json:"product_code"`
	PlanCode     string             `json:"plan_code"`
	BaseQuantity int                `json:"base_quantity"`
	UnitPrice    string             `json:"unit_price"`
	SubTotal     string             `json:"sub_total"`
	Period       SubscriptionPeriod `json:"period"`
}

// ServiceUsage represents usage details for a single service.
type ServiceUsage struct {
	Name         string   `json:"name"`
	Unit         string   `json:"unit"`
	Limit        *float64 `json:"limit"`
	CurrentUsage float64  `json:"current_usage"`
	CurrentCost  string   `json:"current_cost"`
	OverageCost  *string  `json:"overage_cost"`
}

// UsageMetadata holds account-level metadata for a workspace.
type UsageMetadata struct {
	Plan            string `json:"plan"`
	IsSubscribed    bool   `json:"is_subscribed"`
	IsTrialing      bool   `json:"is_trialing"`
	IsPaid          bool   `json:"is_paid"`
	AvailableCredit string `json:"available_credit"`
}

// UsageEstimate represents the full usage estimate response.
type UsageEstimate struct {
	BillingPeriod BillingPeriod           `json:"billing_period"`
	Currency      string                  `json:"currency"`
	Subscription  Subscription            `json:"subscription"`
	Services      map[string]ServiceUsage `json:"services"`
	Metadata      *UsageMetadata          `json:"metadata"`
}

// GetUsageEstimate retrieves the usage estimate for a workspace from the payment API.
func GetUsageEstimate(ac *client.AlpaconClient, paymentBaseURL, workspaceID string) (*UsageEstimate, error) {
	estimateURL := fmt.Sprintf("%s%s%s/estimate/", paymentBaseURL, paymentWorkspacesURL, workspaceID)
	body, err := ac.SendGetRequestToURL(estimateURL)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve usage estimate: %w", err)
	}

	var estimate UsageEstimate
	if err := json.Unmarshal(body, &estimate); err != nil {
		return nil, fmt.Errorf("failed to parse usage estimate: %w", err)
	}

	return &estimate, nil
}
