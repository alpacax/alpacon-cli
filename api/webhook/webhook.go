package webhook

import (
	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	webhookURL = "/api/notifications/webhooks/"
)

func GetWebhookList(ac *client.AlpaconClient) ([]WebhookAttributes, error) {
	webhooks, err := api.FetchAllPages[WebhookResponse](ac, webhookURL, nil)
	if err != nil {
		return nil, err
	}

	var webhookList []WebhookAttributes
	for _, wh := range webhooks {
		webhookList = append(webhookList, WebhookAttributes{
			ID:        wh.ID,
			Name:      wh.Name,
			URL:       wh.URL,
			Provider:  wh.Provider,
			SSLVerify: wh.SSLVerify,
			Enabled:   wh.Enabled,
			Owner:     wh.Owner.Name,
		})
	}

	return webhookList, nil
}

func GetWebhookDetail(ac *client.AlpaconClient, webhookId string) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(webhookURL, webhookId, nil))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func CreateWebhook(ac *client.AlpaconClient, webhookRequest WebhookCreateRequest) error {
	_, err := ac.SendPostRequest(webhookURL, webhookRequest)
	if err != nil {
		return err
	}

	return nil
}

func UpdateWebhook(ac *client.AlpaconClient, webhookName string) ([]byte, error) {
	webhookID, err := GetWebhookIDByName(ac, webhookName)
	if err != nil {
		return nil, err
	}

	responseBody, err := GetWebhookDetail(ac, webhookID)
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(utils.BuildURL(webhookURL, webhookID, nil), data)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func DeleteWebhook(ac *client.AlpaconClient, webhookName string) error {
	webhookID, err := GetWebhookIDByName(ac, webhookName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(utils.BuildURL(webhookURL, webhookID, nil))
	if err != nil {
		return err
	}

	return nil
}

func GetWebhookIDByName(ac *client.AlpaconClient, webhookName string) (string, error) {
	result, err := api.ResolveByName[WebhookResponse](ac, api.ResolveByNameOptions{
		Endpoint:    webhookURL,
		FilterKey:   "name",
		Name:        webhookName,
		BlankMsg:    "webhook name is required",
		NotFoundMsg: "no webhook found with the given name",
	})
	if err != nil {
		return "", err
	}

	return result.ID, nil
}
