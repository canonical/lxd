//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t profiles.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e profile objects version=2
//go:generate mapper stmt -e profile objects-by-ID version=2
//go:generate mapper stmt -e profile objects-by-Project version=2
//go:generate mapper stmt -e profile objects-by-Project-and-Name version=2
//go:generate mapper stmt -e profile id version=2
//go:generate mapper stmt -e profile create version=2
//go:generate mapper stmt -e profile rename version=2
//go:generate mapper stmt -e profile update version=2
//go:generate mapper stmt -e profile delete-by-Project-and-Name version=2
//
//go:generate mapper method -i -e profile ID version=2
//go:generate mapper method -i -e profile Exists version=2
//go:generate mapper method -i -e profile GetMany references=Config,Device version=2
//go:generate mapper method -i -e profile GetOne version=2
//go:generate mapper method -i -e profile Create references=Config,Device version=2
//go:generate mapper method -i -e profile Rename version=2
//go:generate mapper method -i -e profile Update references=Config,Device version=2
//go:generate mapper method -i -e profile DeleteOne-by-Project-and-Name version=2

// Profile is a value object holding db-related details about a profile.
type Profile struct {
	ID          int
	ProjectID   int    `db:"omit=create,update"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
}

// ProfileFilter specifies potential query parameter fields.
type ProfileFilter struct {
	ID      *int
	Project *string
	Name    *string
}
