package kube

import (
	"errors"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const opKubeSessions = "kube_sessions"

func newKubeSessionsCmd(newClient clientFactory) *cobra.Command {
	var (
		clusterFlag string
		all         bool
	)
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List kube sessions",
		Long: `List the open kube sessions you can see, newest first: your own, and on a
cluster you manage, everyone's, which is why each row names its user. Use --all
to include closed sessions.

A session reads as open until the server closes it, which can be a few minutes
after its token has expired.`,
		Example: `  alpacon kube sessions
  alpacon kube sessions --cluster prod-east
  alpacon kube sessions --all --output json`,
		Args: cobra.NoArgs,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			cluster := strings.TrimSpace(clusterFlag)
			if cmd.Flags().Changed("cluster") && cluster == "" {
				return utils.NewUsageEnvelopeExitError(opKubeSessions, "--cluster must name a cluster.")
			}

			ac, err := newClient()
			if err != nil {
				return utils.NewEnvelopeExitError(opKubeSessions, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			rows, err := listKubeSessions(ac, cluster, all)
			if err != nil {
				// The cluster list and the session list answer 404 only when the
				// surface is off. The exit code is named so the refusal, an
				// expected one, leaves out the "report an issue" footer.
				if kubernetes.IsSurfaceOff(err) {
					return utils.NewEnvelopeExitErrorWithCode(utils.ExitCodeGeneralError, opKubeSessions, err, "%s", kubernetes.SurfaceNotEnabledMessage)
				}
				if lookupErr, ok := errors.AsType[*clusterLookupError](err); ok {
					return utils.NewEnvelopeExitError(opKubeSessions, lookupErr.err, "Failed to find the cluster: %s.", lookupErr.err)
				}
				return utils.NewEnvelopeExitError(opKubeSessions, err, "Failed to retrieve the kube sessions: %s.", err)
			}

			utils.PrintTable(rows)
			return nil
		}),
	}
	cmd.Flags().StringVar(&clusterFlag, "cluster", "", "only sessions on this cluster")
	cmd.Flags().BoolVar(&all, "all", false, "include closed sessions")
	return cmd
}

// listKubeSessions reads the rows. A cluster name goes to the server as its ID,
// so the filtering stays on the server. The session list names clusters by ID
// alone, so without --cluster the names come from one read of the cluster list;
// if that fails the rows show the IDs.
func listKubeSessions(ac *client.AlpaconClient, cluster string, all bool) ([]kubernetes.KubeSessionAttributes, error) {
	status := kubernetes.SessionStatusOpen
	if all {
		status = ""
	}

	var clusterID string
	var names map[string]string
	if cluster != "" {
		id, err := kubernetes.GetClusterIDByName(ac, cluster)
		if err != nil {
			return nil, &clusterLookupError{err: err}
		}
		clusterID = id
		names = map[string]string{id: cluster}
	}

	sessions, err := kubernetes.GetSessionList(ac, clusterID, status)
	if err != nil {
		return nil, err
	}

	if cluster == "" && len(sessions) > 0 {
		found, err := kubernetes.GetClusterNames(ac)
		if err != nil {
			utils.CliWarning("Could not resolve cluster names; showing IDs instead: %s", err)
		}
		names = found
	}
	return kubernetes.ProjectSessions(sessions, names, time.Local), nil
}
