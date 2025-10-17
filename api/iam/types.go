package iam

import "time"

type UserAttributes struct {
	Username   string `json:"username"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Tags       string `json:"tags"`
	Groups     int    `json:"groups"`
	UID        int    `json:"uid"`
	Status     string `json:"status"`
	LDAPStatus string `json:"ldap_status"`
}

type UserDetailAttributes struct {
	Username      string   `json:"username"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Email         string   `json:"email"`
	Phone         string   `json:"phone"`
	UID           int      `json:"uid"`
	Shell         string   `json:"shell"`
	HomeDirectory string   `json:"home_directory"`
	NumGroups     int      `json:"num_groups"`
	Groups        []string `json:"groups"`
	Tags          string   `json:"tags"`
	Status        string   `json:"status"`
	LDAPStatus    string   `json:"ldap_status"`
}

type UserResponse struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone"`
	Tags        string    `json:"tags"`
	NumGroups   int       `json:"num_groups"`
	UID         int       `json:"uid"`
	IsActive    bool      `json:"is_active"`
	IsStaff     bool      `json:"is_staff"`
	IsSuperuser bool      `json:"is_superuser"`
	IsLDAPUser  bool      `json:"is_ldap_user"`
	DateJoined  time.Time `json:"date_joined"`
}

type UserSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type UserCreateRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Tags        string `json:"tags"`
	Description string `json:"description"`
	Shell       string `json:"shell"`
	IsActive    bool   `json:"is_active"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
	IsLdapUser  bool   `json:"is_ldap_user"`
}

type GroupAttributes struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Tags        string `json:"tags"`
	Members     int    `json:"members"`
	Servers     int    `json:"servers"`
	GID         int    `json:"gid"`
	LDAPStatus  string `json:"ldap_status"`
}

type GroupResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	DisplayName  string   `json:"display_name"`
	Tags         string   `json:"tags"`
	NumMembers   int      `json:"num_members"`
	GID          int      `json:"gid"`
	IsLDAPGroup  bool     `json:"is_ldap_group"`
	Servers      []string `json:"servers"`
	ServersNames []string `json:"servers_names"`
}

type GroupCreateRequest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Tags        string   `json:"tags"`
	Description string   `json:"description"`
	IsLdapGroup bool     `json:"is_ldap_group"`
	Servers     []string `json:"servers"`
}

type MemberAddRequest struct {
	Group string `json:"group"`
	User  string `json:"user"`
	Role  string `json:"role"`
}

type MemberDetailResponse struct {
	ID        string `json:"id"`
	Group     string `json:"group"`
	GroupName string `json:"group_name"`
	User      string `json:"user"`
	UserName  string `json:"user_name"`
	Role      string `json:"role"`
}

type MemberDeleteRequest struct {
	Group string `json:"group"`
	User  string `json:"user"`
}
