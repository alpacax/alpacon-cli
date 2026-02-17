package webhook

import (
	"encoding/json"
	"errors"

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
	params := map[string]string{
		"name": webhookName,
	}

	responseBody, err := ac.SendGetRequest(utils.BuildURL(webhookURL, "", params))
	if err != nil {
		return "", err
	}

	var response api.ListResponse[WebhookResponse]
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return "", err
	}

	if response.Count == 0 {
		return "", errors.New("no webhook found with the given name")
	}

	return response.Results[0].ID, nil
}
