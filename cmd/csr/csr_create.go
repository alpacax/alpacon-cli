package csr

import (
	"fmt"
	"os/user"
	"path/filepath"

	certApi "github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cert"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	defaultPrivateKeyDir string
	defaultCSRDir        string
)

const (
	infoMessage = "Please specify the paths for the private key and CSR files.\n" +
		"If an existing key is found at the specified path, it will be used.\n" +
		"Otherwise, a new key will be generated.\n" +
		"Note: Root permission may be required for certain paths."
)

var csrCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a CSR",
	Long: `
 	Generates a new Certificate Signing Request based on provided information, 
	which can then be submitted for signing to a certificate authority.
	`,
	Example: `alpacon csr create`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		signRequest, certPath := promptForCert()

		EnsureSecureConnection(alpaconClient)

		response, err := certApi.CreateSignRequest(alpaconClient, signRequest)
		if err != nil {
			utils.CliError("Failed to send sign request to server: %s.", err)
		}

		csr, err := cert.CreateCSR(response, certPath)
		if err != nil {
			utils.CliError("Failed to create CSR file: %s.", err)
		}

		err = certApi.SubmitCSR(alpaconClient, csr, response.SubmitURL)
		if err != nil {
			utils.CliError("Failed to submit CSR file to server: %s.", err)
		}

		utils.CliInfo("CSR creation request succeeded. Check the generated CSR (alpacon csr ls)")
	},
}

func init() {
	usr, err := user.Current()
	if err != nil {
		utils.CliError("Failed to obtain the current user information: %v", err)
	}

	defaultPrivateKeyDir = filepath.Join(usr.HomeDir, "tmp/private/")
	defaultCSRDir = filepath.Join(usr.HomeDir, "tmp/")
}

func promptForCert() (certApi.SignRequest, cert.CertificatePath) {
	var signRequest certApi.SignRequest
	var certPath cert.CertificatePath

	signRequest.DomainList = utils.PromptForListInput("domain list (e.g., domain1.com, domain2.com): ")
	signRequest.IpList = utils.PromptForListInput("ip list (e.g., 192.168.1.1, 10.0.0.1): ")

	if (len(signRequest.DomainList) == 0) && (len(signRequest.IpList) == 0) {
		utils.CliError("You must enter at least a domain list or an IP list.")
	}

	signRequest.ValidDays = utils.PromptForIntInput("valid days (default: 365): ", 365)

	var commonName string
	if len(signRequest.DomainList) > 0 {
		commonName = signRequest.DomainList[0]
	} else {
		commonName = signRequest.IpList[0]
	}

	defaultKeyPath := fmt.Sprintf("%s/%s.key", defaultPrivateKeyDir, commonName)
	defaultCSRPath := fmt.Sprintf("%s/%s.csr", defaultCSRDir, commonName)

	utils.CliInfo(infoMessage)

	certPath.PrivateKeyPath = utils.PromptForInput(fmt.Sprintf("Path for the Private Key file (default: `%s`): ", defaultKeyPath))
	if certPath.PrivateKeyPath == "" {
		certPath.PrivateKeyPath = defaultKeyPath
	}

	certPath.CSRPath = utils.PromptForInput(fmt.Sprintf("Path for the CSR file (default: `%s`): ", defaultCSRPath))
	if certPath.CSRPath == "" {
		certPath.CSRPath = defaultCSRPath
	}

	return signRequest, certPath
}

// EnsureSecureConnection checks if the server uses HTTPS and prompts the user
// to confirm proceeding with an insecure connection if necessary.
func EnsureSecureConnection(client *client.AlpaconClient) {
	isTLS, err := client.IsUsingHTTPS()
	if err != nil {
		utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}
	if !isTLS {
		utils.CliWarning("The connection to %s might not be secure.", client.BaseURL)

		proceed := utils.PromptForBool("Do you want to proceed with the CSR submission?:")
		if !proceed {
			utils.CliError("CSR submission cancelled by user.")
		}

	}
}
