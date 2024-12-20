package storage

import (
	"crypto/rsa"
	"strings"

	"golang.org/x/text/language"
)

// User represents the storage model of a user.
type User struct {
	ID                string
	Username          string
	Password          string
	FirstName         string
	LastName          string
	Email             string
	EmailVerified     bool
	Phone             string
	PhoneVerified     bool
	PreferredLanguage language.Tag
	IsAdmin           bool
}

// Service is the storage service.
type Service struct {
	keys map[string]*rsa.PublicKey
}

// UserStore is the storage interface for users.
type UserStore interface {
	GetUserByID(string) *User
	GetUserByUsername(string) *User
	ExampleClientID() string
}

type userStore struct {
	users map[string]*User
}

// NewUserStore creates a new user store.
func NewUserStore(issuer string) UserStore {
	hostname := strings.Split(strings.Split(issuer, "://")[1], ":")[0]
	return userStore{
		users: map[string]*User{
			"id1": {
				ID:                "id1",
				Username:          "test-user@" + hostname,
				Password:          "verysecure",
				FirstName:         "Test",
				LastName:          "User",
				Email:             "test-user@zitadel.ch",
				EmailVerified:     true,
				Phone:             "",
				PhoneVerified:     false,
				PreferredLanguage: language.German,
				IsAdmin:           true,
			},
			"id2": {
				ID:                "id2",
				Username:          "test-user2",
				Password:          "verysecure",
				FirstName:         "Test",
				LastName:          "User2",
				Email:             "test-user2@zitadel.ch",
				EmailVerified:     true,
				Phone:             "",
				PhoneVerified:     false,
				PreferredLanguage: language.German,
				IsAdmin:           false,
			},
		},
	}
}

// ExampleClientID is only used in the example server.
func (u userStore) ExampleClientID() string {
	return "service"
}

// GetUserByID returns a user by ID.
func (u userStore) GetUserByID(id string) *User {
	return u.users[id]
}

// GetUserByUsername returns a user by username.
func (u userStore) GetUserByUsername(username string) *User {
	for _, user := range u.users {
		if user.Username == username {
			return user
		}
	}
	return nil
}
