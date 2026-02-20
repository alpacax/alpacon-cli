package cert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"

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
	params := map[string]string{
		"status": status,
	}

	csrs, err := api.FetchAllPages[CSRResponse](ac, signRequestURL, params)
	if err != nil {
		return nil, err
	}

	var csrList []CSRAttributes
	for _, csr := range csrs {
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

	return csrList, nil
}

func GetAuthorityList(ac *client.AlpaconClient) ([]AuthorityAttributes, error) {
	authorities, err := api.FetchAllPages[AuthorityResponse](ac, authorityURL, nil)
	if err != nil {
		return nil, err
	}

	var authorityList []AuthorityAttributes
	for _, authority := range authorities {
		authorityList = append(authorityList, AuthorityAttributes{
			Id:               authority.Id,
			Name:             authority.Name,
			Organization:     authority.Organization,
			Domain:           authority.Domain,
			RootValidDays:    authority.RootValidDays,
			DefaultValidDays: authority.DefaultValidDays,
			MaxValidDays:     authority.MaxValidDays,
			Server:           authority.Agent.Name,
			Owner:            authority.Owner.Name,
			SignedAt:         utils.TimeUtils(authority.SignedAt),
		})
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

func RetryCSR(ac *client.AlpaconClient, csrId string) ([]byte, error) {
	relativePath := path.Join(csrId, "retry")
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

	return nil
}

func DeleteCA(ac *client.AlpaconClient, authorityId string) error {
	_, err := ac.SendDeleteRequest(utils.BuildURL(authorityURL, authorityId, nil))
	if err != nil {
		return err
	}

	return nil
}

func GetCertificateList(ac *client.AlpaconClient) ([]CertificateAttributes, error) {
	certs, err := api.FetchAllPages[Certificate](ac, certURL, nil)
	if err != nil {
		return nil, err
	}

	var certList []CertificateAttributes
	for _, cert := range certs {
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

	return certList, nil
}

func DownloadCertificateByCSR(ac *client.AlpaconClient, csrId string, filePath string) error {
	body, err := GetCSRDetail(ac, csrId)
	if err != nil {
		return err
	}

	var detail SignRequestDetail
	if err = json.Unmarshal(body, &detail); err != nil {
		return err
	}

	if detail.Status != "signed" {
		return fmt.Errorf("certificate not yet issued for this CSR (status: %s)", detail.Status)
	}

	if detail.CrtText == "" {
		return fmt.Errorf("certificate text is empty for signed CSR (id: %s)", detail.Id)
	}

	return utils.SaveFile(filePath, []byte(detail.CrtText))
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

func UpdateAuthority(ac *client.AlpaconClient, authorityId string) ([]byte, error) {
	responseBody, err := GetAuthorityDetail(ac, authorityId)
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(utils.BuildURL(authorityURL, authorityId, nil), data)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func DownloadCRL(ac *client.AlpaconClient, authorityId string, filePath string) error {
	relativePath := path.Join(authorityId, "crl")
	body, err := ac.SendGetRequest(utils.BuildURL(authorityURL, relativePath, nil))
	if err != nil {
		return err
	}

	var response CRLResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	return utils.SaveFile(filePath, []byte(response.CrlText))
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
