package packages

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"path/filepath"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	systemPackageEntryURL = "/api/packages/system/entries/"
	pythonPackageEntryURL = "/api/packages/python/entries/"
)

func GetSystemPackageEntry(ac *client.AlpaconClient) ([]SystemPackage, error) {
	packages, err := api.FetchAllPages[SystemPackageDetail](ac, systemPackageEntryURL, nil)
	if err != nil {
		return nil, err
	}

	var packageList []SystemPackage
	for _, pkg := range packages {
		packageList = append(packageList, SystemPackage{
			Name:     pkg.Name,
			Version:  pkg.Version,
			Arch:     pkg.Arch,
			Platform: pkg.Platform,
			Owner:    pkg.Owner.Name,
		})
	}
	return packageList, nil
}

func GetPythonPackageEntry(ac *client.AlpaconClient) ([]PythonPackage, error) {
	packages, err := api.FetchAllPages[PythonPackageDetail](ac, pythonPackageEntryURL, nil)
	if err != nil {
		return nil, err
	}

	var packageList []PythonPackage
	for _, pkg := range packages {
		packageList = append(packageList, PythonPackage{
			Name:         pkg.Name,
			Version:      pkg.Version,
			PythonTarget: pkg.Target,
			ABI:          pkg.ABI,
			Platform:     pkg.Platform,
			Owner:        pkg.Owner.Name,
		})
	}
	return packageList, nil
}

func GetPackageIDByName(ac *client.AlpaconClient, fileName string, packageType string) (string, error) {
	var url string
	params := map[string]string{
		"name": fileName,
	}

	if packageType == "python" {
		url = pythonPackageEntryURL
		var response api.ListResponse[PythonPackageDetail]
		body, err := ac.SendGetRequest(utils.BuildURL(url, "", params))
		if err != nil {
			return "", err
		}
		err = json.Unmarshal(body, &response)
		if err != nil {
			return "", err
		}

		if response.Count == 0 {
			return "", errors.New("no package found with the given name")
		}
		return response.Results[0].ID, nil
	} else {
		url = systemPackageEntryURL
		var response api.ListResponse[SystemPackageDetail]
		body, err := ac.SendGetRequest(utils.BuildURL(url, "", params))
		if err != nil {
			return "", err
		}
		err = json.Unmarshal(body, &response)
		if err != nil {
			return "", err
		}

		if response.Count == 0 {
			return "", errors.New("no package found with the given name")
		}
		return response.Results[0].ID, nil
	}
}

func UploadPackage(ac *client.AlpaconClient, file string, packageType string) error {
	content, err := utils.ReadFileFromPath(file)
	if err != nil {
		return err
	}

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	fileWriter, err := writer.CreateFormFile("content", file)
	if err != nil {
		return err
	}
	_, err = fileWriter.Write(content)
	if err != nil {
		return err
	}
	_ = writer.Close()

	var requestURL string
	if packageType == "python" {
		requestURL = pythonPackageEntryURL
	} else {
		requestURL = systemPackageEntryURL
	}

	_, err = ac.SendMultipartRequest(requestURL, writer, requestBody)
	if err != nil {
		return err
	}

	return nil
}

func DownloadPackage(ac *client.AlpaconClient, fileName string, dest string, packageType string) error {
	packageID, err := GetPackageIDByName(ac, fileName, packageType)
	if err != nil {
		return err
	}

	var url string
	if packageType == "python" {
		url = pythonPackageEntryURL
	} else {
		url = systemPackageEntryURL
	}

	respBody, err := ac.SendGetRequest(utils.BuildURL(url, packageID, nil))
	if err != nil {
		return err
	}

	var downloadURL DownloadURL
	err = json.Unmarshal(respBody, &downloadURL)
	if err != nil {
		return err
	}

	resp, err := ac.SendGetRequestForDownload(utils.RemovePrefixBeforeAPI(downloadURL.DownloadURL))
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	savePath := filepath.Join(dest, filepath.Base(fileName))
	err = utils.SaveFile(savePath, respBody)
	if err != nil {
		return err
	}

	return nil
}
