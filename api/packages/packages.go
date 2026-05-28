package packages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	systemPackageEntryURL = "/api/packages/system/entries/"
	pythonPackageEntryURL = "/api/packages/python/entries/"
)

func packageEntryURL(packageType string) string {
	if packageType == "python" {
		return pythonPackageEntryURL
	}
	return systemPackageEntryURL
}

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
	params := map[string]string{"name": fileName}
	body, err := ac.SendGetRequest(utils.BuildURL(packageEntryURL(packageType), "", params))
	if err != nil {
		return "", err
	}

	if packageType == "python" {
		var response api.ListResponse[PythonPackageDetail]
		if err := json.Unmarshal(body, &response); err != nil {
			return "", err
		}
		if response.Count == 0 {
			return "", errors.New("no package found with the given name")
		}
		return response.Results[0].ID, nil
	}

	var response api.ListResponse[SystemPackageDetail]
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if response.Count == 0 {
		return "", errors.New("no package found with the given name")
	}
	return response.Results[0].ID, nil
}

func UploadPackage(ac *client.AlpaconClient, file string, packageType string) error {
	src, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	var contentType string
	requestBody, size, err := utils.SpoolToTempFile("alpacon-package-upload-*.multipart", func(w io.Writer) error {
		writer := multipart.NewWriter(w)
		fileWriter, err := writer.CreateFormFile("content", file)
		if err != nil {
			return err
		}
		if _, err := io.Copy(fileWriter, src); err != nil {
			return err
		}
		contentType = writer.FormDataContentType()
		return writer.Close()
	})
	if err != nil {
		return err
	}
	defer utils.CleanupTempFile(requestBody)

	_, err = ac.SendMultipartStreamRequest(packageEntryURL(packageType), contentType, requestBody, size)
	if err != nil {
		return err
	}

	return nil
}

func packageDownloadResponseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if message := strings.TrimSpace(string(body)); message != "" {
		return fmt.Errorf("package download failed with status %d: %s", resp.StatusCode, message)
	}
	return fmt.Errorf("package download failed with status %d", resp.StatusCode)
}

func DownloadPackage(ac *client.AlpaconClient, fileName string, dest string, packageType string) error {
	packageID, err := GetPackageIDByName(ac, fileName, packageType)
	if err != nil {
		return err
	}

	respBody, err := ac.SendGetRequest(utils.BuildURL(packageEntryURL(packageType), packageID, nil))
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

	if resp.StatusCode != http.StatusOK {
		return packageDownloadResponseError(resp)
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return fmt.Errorf("package download returned JSON instead of file content")
	}

	savePath := filepath.Join(dest, filepath.Base(fileName))
	if _, err = utils.SaveStreamAtomic(savePath, resp.Body); err != nil {
		return err
	}

	return nil
}
