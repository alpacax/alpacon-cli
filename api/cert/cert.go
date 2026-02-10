package cert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strconv"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	authorityURL   = "/api/cert/authorities/"
	signRequestURL = "/api/cert/sign-requests/"
	certURL        = "/api/cert/certificates/"
)

func CreateSignRequest(ac *client.AlpaconClient, signRequest SignRequest) (SignRequestResponse, error) {
	var response SignRequestResponse

	responseBody, err := ac.SendPostRequest(signRequestURL, signRequest)
	if err != nil {
		return SignRequestResponse{}, err
	}

	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return SignRequestResponse{}, err
	}

	return response, nil
}

func SubmitCSR(ac *client.AlpaconClient, csr []byte, submitURL string) error {
	var request CSRSubmit
	request.CsrText = string(csr)

	_, err := ac.SendPatchRequest(submitURL, request)
	if err != nil {
		return err
	}

	return nil
}

func CreateAuthority(ac *client.AlpaconClient, authorityRequest AuthorityRequest) (AuthorityCreateResponse, error) {
	var response AuthorityCreateResponse
	responseBody, err := ac.SendPostRequest(authorityURL, authorityRequest)
	if err != nil {
		return AuthorityCreateResponse{}, err
	}

	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return AuthorityCreateResponse{}, err
	}

	return response, nil
}

func GetCSRList(ac *client.AlpaconClient, status string) ([]CSRAttributes, error) {
	var csrList []CSRAttributes
	page := 1
	const pageSize = 100

	params := map[string]string{
		"status":    status,
		"page":      strconv.Itoa(page),
		"page_size": fmt.Sprintf("%d", pageSize),
	}

	for {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(signRequestURL, "", params))
		if err != nil {
			return nil, err
		}

		var response api.ListResponse[CSRResponse]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, err
		}

		for _, csr := range response.Results {
			csrList = append(csrList, CSRAttributes{
				Id:            csr.Id,
				Name:          csr.CommonName,
				Authority:     csr.Authority.Name,
				DomainList:    csr.DomainList,
				IpList:        csr.IpList,
				Status:        csr.Status,
				RequestedIp:   csr.RequestedIp,
				RequestedBy:   csr.RequestedBy.Name,
				RequestedDate: utils.TimeUtils(csr.AddedAt),
			})
		}

		if len(response.Results) < pageSize {
			break
		}
		page++
		params["page"] = strconv.Itoa(page)
	}

	return csrList, nil
}

func GetAuthorityList(ac *client.AlpaconClient) ([]AuthorityAttributes, error) {
	var authorityList []AuthorityAttributes
	page := 1
	const pageSize = 100

	params := map[string]string{
		"page":      strconv.Itoa(page),
		"page_size": fmt.Sprintf("%d", pageSize),
	}

	for {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(authorityURL, "", params))
		if err != nil {
			return nil, err
		}

		var response api.ListResponse[AuthorityResponse]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, err
		}

		for _, authority := range response.Results {
			authorityList = append(authorityList, AuthorityAttributes{
				Id:               authority.Id,
				Name:             authority.Name,
				Organization:     authority.Organization,
				Domain:           authority.Domain,
				RootValidDays:    authority.RootValidDays,
				DefaultValidDays: authority.DefaultValidDays,
				MaxValidDays:     authority.MaxValidDays,
				Server:           authority.AgentName,
				Owner:            authority.Owner.Name,
				SignedAt:         utils.TimeUtils(authority.SignedAt),
			})
		}

		if len(response.Results) < pageSize {
			break
		}
		page++
		params["page"] = strconv.Itoa(page)
	}

	return authorityList, nil
}

func GetAuthorityDetail(ac *client.AlpaconClient, authorityId string) ([]byte, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(authorityURL, authorityId, nil))
	if err != nil {
		return nil, err
	}

	return body, nil
}

func GetCSRDetail(ac *client.AlpaconClient, csrId string) ([]byte, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(signRequestURL, csrId, nil))
	if err != nil {
		return nil, err
	}

	return body, nil
}

func GetCertificateDetail(ac *client.AlpaconClient, certId string) ([]byte, error) {
	body, err := ac.SendGetRequest(utils.BuildURL(certURL, certId, nil))
	if err != nil {
		return nil, err
	}

	return body, nil
}

func ApproveCSR(ac *client.AlpaconClient, csrId string) ([]byte, error) {
	relativePath := path.Join(csrId, "approve")
	responseBody, err := ac.SendPostRequest(utils.BuildURL(signRequestURL, relativePath, nil), bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func DenyCSR(ac *client.AlpaconClient, csrId string) ([]byte, error) {
	relativePath := path.Join(csrId, "deny")
	responseBody, err := ac.SendPostRequest(utils.BuildURL(signRequestURL, relativePath, nil), bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func DeleteCSR(ac *client.AlpaconClient, csrId string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(signRequestURL, csrId, nil))
	if err != nil {
		return err
	}

	return err
}

func DeleteCA(ac *client.AlpaconClient, authorityId string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(authorityURL, authorityId, nil))
	if err != nil {
		return err
	}

	return err
}

func GetCertificateList(ac *client.AlpaconClient) ([]CertificateAttributes, error) {
	var certList []CertificateAttributes
	page := 1
	const pageSize = 100

	params := map[string]string{
		"page":      strconv.Itoa(page),
		"page_size": fmt.Sprintf("%d", pageSize),
	}
	for {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(certURL, "", params))
		if err != nil {
			return nil, err
		}

		var response api.ListResponse[Certificate]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, err
		}

		for _, cert := range response.Results {
			certList = append(certList, CertificateAttributes{
				Id:        cert.Id,
				Authority: cert.Authority.Name,
				Csr:       cert.Csr,
				ValidDays: cert.ValidDays,
				SignedAt:  utils.TimeUtils(cert.SignedAt),
				ExpiresAt: utils.TimeUtils(cert.ExpiresAt),
				SignedBy:  cert.SignedBy,
				RenewedBy: cert.RenewedBy,
			})
		}

		if len(response.Results) < pageSize {
			break
		}
		page++
		params["page"] = strconv.Itoa(page)
	}

	return certList, nil
}

func DownloadCertificate(ac *client.AlpaconClient, certId string, filePath string) error {
	body, err := GetCertificateDetail(ac, certId)
	if err != nil {
		return err
	}

	var response Certificate
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	err = utils.SaveFile(filePath, []byte(response.CrtText))
	if err != nil {
		return err
	}

	return nil
}

func DownloadRootCertificate(ac *client.AlpaconClient, authorityId string, filePath string) error {
	body, err := ac.SendGetRequest(utils.BuildURL(authorityURL, authorityId, nil))
	if err != nil {
		return err
	}

	var response AuthorityDetails
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	err = utils.SaveFile(filePath, []byte(response.CrtText))
	if err != nil {
		return err
	}

	return nil
}
