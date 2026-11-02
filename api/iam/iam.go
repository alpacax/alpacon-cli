package iam

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	userURL       = "/api/iam/users/"
	groupURL      = "/api/iam/groups/"
	membershipURL = "/api/iam/memberships/"
	usernameURL   = "/api/iam/username/"
	inviteUserURL = "/api/workspaces/users/invite/"

	codeUsernameInvalid    = "user_username_invalid"
	codeUsernameDisallowed = "user_username_disallowed"
	codeUsernameInUse      = "user_username_in_use"
	codeUsernameAlreadySet = "user_username_already_set"
	codeUsernameApproval   = "approval_superuser_approve_required"
)

// UsernameSetSuccessFmt is the confirmation format shown after a username is set.
const UsernameSetSuccessFmt = "Username set to %q"

// These mirror the account's roles and are read-only: a PATCH naming one is answered
// 200 with the flag unchanged, never an error, so the CLI has to catch the edit itself.
var privilegeFlagFields = []string{"is_staff", "is_superuser"}

// usernameErrors maps each server username error code to its message and whether another name
// can resolve it.
var usernameErrors = map[string]struct {
	message   string
	retryable bool
}{
	codeUsernameInvalid:    {"invalid username format: use lowercase letters, digits, '-', '_', and start with a letter", true},
	codeUsernameDisallowed: {"this username is reserved and cannot be used (e.g. alpacon, root, admin)", true},
	codeUsernameInUse:      {"this username is already in use", true},
	codeUsernameAlreadySet: {"username is already set and cannot be changed here", false},
	codeUsernameApproval:   {"this username conflicts with an existing system account and is pending superuser approval", false},
}

func GetUserList(ac *client.AlpaconClient) ([]UserAttributes, error) {
	users, err := api.FetchAllPages[UserResponse](ac, userURL, nil)
	if err != nil {
		return nil, err
	}

	var userList []UserAttributes
	for _, user := range users {
		userList = append(userList, UserAttributes{
			Username:   user.Username,
			Name:       fmt.Sprintf("%s %s", user.LastName, user.FirstName),
			Email:      user.Email,
			Tags:       user.Tags,
			Groups:     user.NumGroups,
			UID:        user.UID,
			Status:     getUserStatus(user.IsActive, user.IsStaff, user.IsSuperuser),
			LDAPStatus: getLDAPStatus(user.IsLDAPUser),
		})
	}
	return userList, nil
}

func GetGroupList(ac *client.AlpaconClient) ([]GroupAttributes, error) {
	groups, err := api.FetchAllPages[GroupResponse](ac, groupURL, nil)
	if err != nil {
		return nil, err
	}

	var groupList []GroupAttributes
	for _, group := range groups {
		groupList = append(groupList, GroupAttributes{
			Name:        group.Name,
			DisplayName: group.DisplayName,
			Tags:        group.Tags,
			Members:     group.NumMembers,
			Servers:     len(group.Servers),
			GID:         group.GID,
			LDAPStatus:  getLDAPStatus(group.IsLDAPGroup),
		})
	}
	return groupList, nil
}

func GetUserDetail(ac *client.AlpaconClient, userId string) ([]byte, error) {
	pathID, err := api.RequireID(userId, "user")
	if err != nil {
		return nil, err
	}
	return getUserDetail(ac, pathID)
}

func getUserDetail(ac *client.AlpaconClient, pathID api.PathID) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(api.IDURL(userURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func GetGroupDetail(ac *client.AlpaconClient, groupId string) ([]byte, error) {
	pathID, err := api.RequireID(groupId, "group")
	if err != nil {
		return nil, err
	}
	return getGroupDetail(ac, pathID)
}

func getGroupDetail(ac *client.AlpaconClient, pathID api.PathID) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(api.IDURL(groupURL, pathID, "", nil))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func InviteUser(ac *client.AlpaconClient, request UserInviteRequest) error {
	_, err := ac.SendPostRequest(inviteUserURL, request)
	return err
}

// CreateUser returns the password the server generated for the new user, or "" when the response carries none.
func CreateUser(ac *client.AlpaconClient, userRequest UserCreateRequest) (string, error) {
	userRequest.IsActive = true
	body, err := ac.SendPostRequest(userURL, userRequest)
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", nil
	}

	var created struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return "", err
	}

	return created.Password, nil
}

func CreateGroup(ac *client.AlpaconClient, groupRequest GroupCreateRequest) error {
	_, err := ac.SendPostRequest(groupURL, groupRequest)
	if err != nil {
		return err
	}

	return nil
}

func DeleteUser(ac *client.AlpaconClient, userName string) error {
	pathID, err := userPathIDByName(ac, userName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(api.IDURL(userURL, pathID, "", nil))
	if err != nil {
		return err
	}

	return nil
}

func DeleteGroup(ac *client.AlpaconClient, groupName string) error {
	pathID, err := groupPathIDByName(ac, groupName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(api.IDURL(groupURL, pathID, "", nil))
	if err != nil {
		return err
	}

	return nil
}

func AddMember(ac *client.AlpaconClient, memberRequest MemberAddRequest) error {
	var err error
	memberRequest.Group, err = GetGroupIDByName(ac, memberRequest.Group)
	if err != nil {
		return err
	}

	memberRequest.User, err = GetUserIDByName(ac, memberRequest.User)
	if err != nil {
		return err
	}

	_, err = ac.SendPostRequest(membershipURL, memberRequest)
	if err != nil {
		return err
	}

	return nil
}

func DeleteMember(ac *client.AlpaconClient, memberDeleteRequest MemberDeleteRequest) error {
	groupID, err := GetGroupIDByName(ac, memberDeleteRequest.Group)
	if err != nil {
		return err
	}

	memberID, err := GetUserIDByName(ac, memberDeleteRequest.User)
	if err != nil {
		return err
	}

	params := map[string]string{
		"user":  memberID,
		"group": groupID,
	}
	memberDetails, err := api.FetchPagesUpTo[MemberDetailResponse](ac, membershipURL, params, 1)
	if err != nil {
		return err
	}
	if len(memberDetails) == 0 {
		return errors.New("no membership found for the given user and group")
	}

	pathID, err := api.RequireResponseID(memberDetails[0].ID, "membership")
	if err != nil {
		return err
	}
	_, err = ac.SendDeleteRequest(api.IDURL(membershipURL, pathID, "", nil))
	if err != nil {
		return err
	}

	return nil
}

func GetUserIDByName(ac *client.AlpaconClient, userName string) (string, error) {
	pathID, err := userPathIDByName(ac, userName)
	if err != nil {
		return "", err
	}
	return pathID.String(), nil
}

func userPathIDByName(ac *client.AlpaconClient, userName string) (api.PathID, error) {
	result, err := api.ResolveByName[UserResponse](ac, api.ResolveByNameOptions{
		Endpoint:    userURL,
		FilterKey:   "username",
		Name:        userName,
		BlankMsg:    "username is required",
		NotFoundMsg: "no user found with the given name",
	})
	if err != nil {
		return api.PathID{}, err
	}

	return api.RequireResponseID(result.ID, "user")
}

// GetUsernamesByID maps each user's id to the username other commands accept; a role binding
// embeds only a display name.
func GetUsernamesByID(ac *client.AlpaconClient) (map[string]string, error) {
	users, err := api.FetchAllPages[UserResponse](ac, userURL, nil)
	if err != nil {
		return nil, err
	}

	usernames := make(map[string]string, len(users))
	for _, user := range users {
		usernames[user.ID] = user.Username
	}

	return usernames, nil
}

func GetGroupIDByName(ac *client.AlpaconClient, groupName string) (string, error) {
	pathID, err := groupPathIDByName(ac, groupName)
	if err != nil {
		return "", err
	}
	return pathID.String(), nil
}

func groupPathIDByName(ac *client.AlpaconClient, groupName string) (api.PathID, error) {
	result, err := api.ResolveByName[GroupResponse](ac, api.ResolveByNameOptions{
		Endpoint:    groupURL,
		FilterKey:   "name",
		Name:        groupName,
		BlankMsg:    "group name is required",
		NotFoundMsg: "no group found with the given name",
	})
	if err != nil {
		return api.PathID{}, err
	}

	return api.RequireResponseID(result.ID, "group")
}

func getUserStatus(isActive bool, isStaff bool, isSuperuser bool) string {
	if isSuperuser {
		return "superuser"
	}
	if isStaff {
		return "staff"
	}
	if isActive {
		return "active"
	}
	return "inactive"
}

func getLDAPStatus(isLDAP bool) string {
	if isLDAP {
		return "ldap"
	}

	return "local"
}

func UpdateGroup(ac *client.AlpaconClient, groupName string) ([]byte, error) {
	pathID, err := groupPathIDByName(ac, groupName)
	if err != nil {
		return nil, err
	}

	responseBody, err := getGroupDetail(ac, pathID)
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(api.IDURL(groupURL, pathID, "", nil), data)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

// PrepareUserUpdate opens the user's detail in an editor and reports what changed; it
// sends nothing. The patch is sparse so untouched fields are not re-submitted:
// forwarding an unchanged is_ldap_user makes the server run a live LDAP bind.
func PrepareUserUpdate(ac *client.AlpaconClient, userName string) (string, *UserEdit, error) {
	pathID, err := userPathIDByName(ac, userName)
	if err != nil {
		return "", nil, err
	}

	responseBody, err := getUserDetail(ac, pathID)
	if err != nil {
		return "", nil, err
	}

	edited, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return "", nil, err
	}

	edit, err := diffEditedUser(responseBody, edited)
	if err != nil {
		return "", nil, err
	}

	return pathID.String(), edit, nil
}

func PatchUser(ac *client.AlpaconClient, userID string, changes map[string]any) ([]byte, error) {
	pathID, err := api.RequireID(userID, "user")
	if err != nil {
		return nil, err
	}
	return ac.SendPatchRequest(api.IDURL(userURL, pathID, "", nil), changes)
}

// diffEditedUser counts a key the operator deleted as untouched—a PATCH names what to change, and
// nothing spells "unset" that the server would honor.
func diffEditedUser(original []byte, edited any) (*UserEdit, error) {
	var before map[string]any
	if err := json.Unmarshal(original, &before); err != nil {
		return nil, err
	}

	after, ok := edited.(map[string]any)
	if !ok {
		return nil, errors.New("the edited file must contain a JSON object")
	}

	edit := &UserEdit{Changes: map[string]any{}}
	for _, field := range privilegeFlagFields {
		value, present := after[field]
		if !present || reflect.DeepEqual(before[field], value) {
			continue
		}

		want, isBool := value.(bool)
		if !isBool {
			return nil, fmt.Errorf("%s must be true or false", field)
		}
		edit.Privileges = append(edit.Privileges, PrivilegeEdit{Field: field, Enable: want})
	}

	for field, value := range after {
		if slices.Contains(privilegeFlagFields, field) {
			continue
		}
		if !reflect.DeepEqual(before[field], value) {
			edit.Changes[field] = value
		}
	}

	return edit, nil
}

func GetCurrentUser(ac *client.AlpaconClient) (*CurrentUserResponse, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(userURL, "-", nil))
	if err != nil {
		return nil, err
	}

	var user CurrentUserResponse
	err = json.Unmarshal(responseBody, &user)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func GetUserMemberships(ac *client.AlpaconClient, userID string) ([]GroupMembership, error) {
	params := map[string]string{
		"user": userID,
	}

	members, err := api.FetchAllPages[MemberDetailResponse](ac, membershipURL, params)
	if err != nil {
		return nil, err
	}

	var groups []GroupMembership
	for _, m := range members {
		groups = append(groups, GroupMembership{
			Name: m.GroupName,
			Role: m.Role,
		})
	}

	return groups, nil
}

func HandleUsernameRequired() (*SetUsernameResponse, error) {
	if !utils.IsInteractiveShell() {
		return nil, errors.New("username is not set for your account; run 'alpacon username set <name>' to set it (lowercase letters, digits, '-', '_'; must start with a letter)")
	}

	utils.CliInfo("Username is required for your account.")
	utils.CliInfo("You can also set it anytime with 'alpacon username set <name>'.")

	for {
		username := utils.PromptForRequiredInput("Please enter your username: ")

		response, err := SetUsername(username)
		if err == nil {
			utils.CliSuccess(UsernameSetSuccessFmt, response.Username)
			return response, nil
		}

		warning, alreadySet, stopErr := usernameFailure(err)
		switch {
		case warning != "":
			utils.CliWarning("%s", warning)
			continue
		case alreadySet:
			// Set concurrently, e.g. by another session; treat as success so the command proceeds.
			utils.CliInfo("Username is already set; continuing.")
			return nil, nil
		default:
			return nil, stopErr
		}
	}
}

// usernameFailure decides what the username prompt does with a failed
// SetUsername: ask again after a warning, carry on because the username is
// already set, or stop with an error. A refusal carrying the server's hint
// stops with that refusal kept in the chain, so the hint prints under it in
// place of the local message.
func usernameFailure(err error) (warning string, alreadySet bool, stop error) {
	code, _ := utils.ParseErrorResponse(err)
	msg, known := UsernameErrorMessage(code)
	switch {
	case !known:
		return "", false, fmt.Errorf("failed to set username: %w", err)
	case isRetryableUsernameError(code):
		return msg, false, nil
	case code == codeUsernameAlreadySet:
		return "", true, nil
	case utils.RefusalHint(err) != "":
		return "", false, fmt.Errorf("failed to set username: %w", err)
	default:
		return "", false, errors.New(msg)
	}
}

func SetUsername(username string) (*SetUsernameResponse, error) {
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	request := SetUsernameRequest{
		Username: username,
	}

	responseBody, err := ac.SendPostRequest(usernameURL, request)
	if err != nil {
		return nil, err
	}

	var response SetUsernameResponse
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

// UsernameErrorMessage maps a server username error code to a human-readable message, reporting
// false for an unrecognized code.
func UsernameErrorMessage(code string) (string, bool) {
	info, ok := usernameErrors[code]
	return info.message, ok
}

func isRetryableUsernameError(code string) bool {
	return usernameErrors[code].retryable
}
