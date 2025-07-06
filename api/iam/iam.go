package iam

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	userURL       = "/api/iam/users/"
	groupURL      = "/api/iam/groups/"
	membershipURL = "/api/iam/memberships/"
)

func GetUserList(ac *client.AlpaconClient) ([]UserAttributes, error) {
	var userList []UserAttributes
	page := 1
	const pageSize = 100

	params := map[string]string{
		"page":      strconv.Itoa(page),
		"page_size": fmt.Sprintf("%d", pageSize),
	}
	for {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(userURL, "", params))
		if err != nil {
			return nil, err
		}

		var response api.ListResponse[UserResponse]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, err
		}

		for _, user := range response.Results {
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

		if len(response.Results) < pageSize {
			break
		}
		page++
	}
	return userList, nil
}

func GetGroupList(ac *client.AlpaconClient) ([]GroupAttributes, error) {
	var groupList []GroupAttributes
	page := 1
	const pageSize = 100

	params := map[string]string{
		"page":      strconv.Itoa(page),
		"page_size": fmt.Sprintf("%d", pageSize),
	}
	for {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(groupURL, "", params))
		if err != nil {
			return nil, err
		}

		var response api.ListResponse[GroupResponse]
		if err = json.Unmarshal(responseBody, &response); err != nil {
			return nil, err
		}

		for _, group := range response.Results {
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

		if len(response.Results) < pageSize {
			break
		}
		page++
	}
	return groupList, nil
}

func GetUserDetail(ac *client.AlpaconClient, userId string) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(userURL, userId, nil))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func GetGroupDetail(ac *client.AlpaconClient, groupId string) ([]byte, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(groupURL, groupId, nil))
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func CreateUser(ac *client.AlpaconClient, userRequest UserCreateRequest) error {
	userRequest.IsActive = true
	_, err := ac.SendPostRequest(userURL, userRequest)
	if err != nil {
		return err
	}

	return nil
}

func CreateGroup(ac *client.AlpaconClient, groupRequest GroupCreateRequest) error {
	_, err := ac.SendPostRequest(groupURL, groupRequest)
	if err != nil {
		return err
	}

	return nil
}

func DeleteUser(ac *client.AlpaconClient, userName string) error {
	userID, err := GetUserIDByName(ac, userName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(utils.BuildURL(userURL, userID, nil))
	if err != nil {
		return err
	}

	return err
}

func DeleteGroup(ac *client.AlpaconClient, groupName string) error {
	groupID, err := GetGroupIDByName(ac, groupName)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(utils.BuildURL(groupURL, groupID, nil))
	if err != nil {
		return err
	}

	return err
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
	responseBody, err := ac.SendGetRequest(utils.BuildURL(membershipURL, "", params))
	if err != nil {
		return err
	}

	var memberDetails []MemberDetailResponse
	err = json.Unmarshal(responseBody, &memberDetails)
	if err != nil {
		return err
	}

	_, err = ac.SendDeleteRequest(utils.BuildURL(membershipURL, memberDetails[0].ID, nil))
	if err != nil {
		return err
	}

	return err
}

func GetUserIDByName(ac *client.AlpaconClient, userName string) (string, error) {
	params := map[string]string{
		"username": userName,
	}

	responseBody, err := ac.SendGetRequest(utils.BuildURL(userURL, "", params))
	if err != nil {
		return "", err
	}

	var response api.ListResponse[UserResponse]
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return "", err
	}

	if response.Count == 0 {
		return "", errors.New("no user found with the given name")
	}

	return response.Results[0].ID, nil
}

func GetUserNameByID(ac *client.AlpaconClient, userID string) (string, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(userURL, userID, nil))
	if err != nil {
		return "", err
	}

	var response UserDetailAttributes
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return "", err
	}

	return response.Username, nil
}

func GetGroupIDByName(ac *client.AlpaconClient, groupName string) (string, error) {
	params := map[string]string{
		"name": groupName,
	}
	responseBody, err := ac.SendGetRequest(utils.BuildURL(groupURL, "", params))
	if err != nil {
		return "", err
	}

	var response api.ListResponse[GroupResponse]
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return "", err
	}

	if response.Count == 0 {
		return "", errors.New("no group found with the given name")
	}

	return response.Results[0].ID, nil
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

func UpdateUser(ac *client.AlpaconClient, userName string) ([]byte, error) {
	userId, err := GetUserIDByName(ac, userName)
	if err != nil {
		return nil, err
	}

	responseBody, err := GetUserDetail(ac, userId)
	if err != nil {
		return nil, err
	}

	data, err := utils.ProcessEditedData(responseBody)
	if err != nil {
		return nil, err
	}

	responseBody, err = ac.SendPatchRequest(utils.BuildURL(userURL, userId, nil), data)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}
