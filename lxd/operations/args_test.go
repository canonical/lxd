package operations

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

type argsSuite struct {
	suite.Suite
}

func TestArgsSuite(t *testing.T) {
	suite.Run(t, new(argsSuite))
}

// validTaskOperationArgs returns a valid OperationArgs for a task operation.
func validTaskOperationArgs() OperationArgs {
	return OperationArgs{
		ProjectName: "default",
		Type:        operationtype.InstanceCreate,
		Class:       operationtype.OperationClassTask,
		EntityURL:   entity.ProjectURL("default"),
		RunHook: func(ctx context.Context, op *Operation) error {
			return nil
		},
	}
}

// validWebsocketOperationArgs returns a valid OperationArgs for a websocket operation.
func validWebsocketOperationArgs() OperationArgs {
	return OperationArgs{
		ProjectName: "default",
		Type:        operationtype.ConsoleShow,
		Class:       operationtype.OperationClassWebsocket,
		EntityURL:   entity.InstanceURL("default", "test-instance"),
		ConnectHook: func(op *Operation, r *http.Request, w http.ResponseWriter) error {
			return nil
		},
	}
}

// validTokenOperationArgs returns a valid OperationArgs for a token operation.
func validTokenOperationArgs() OperationArgs {
	return OperationArgs{
		ProjectName: "default",
		Type:        operationtype.ClusterJoinToken,
		Class:       operationtype.OperationClassToken,
		EntityURL:   entity.ServerURL(),
	}
}

// validServerLevelOperationArgs returns a valid server-level task operation.
func validServerLevelOperationArgs() OperationArgs {
	return OperationArgs{
		ProjectName: "",
		Type:        operationtype.ClusterBootstrap,
		Class:       operationtype.OperationClassTask,
		EntityURL:   nil,
		RunHook: func(ctx context.Context, op *Operation) error {
			return nil
		},
	}
}

func (s *argsSuite) TestValidate() {
	type testCase struct {
		name      string
		args      OperationArgs
		isChild   bool
		expectErr bool
		errMsg    string
	}

	tests := []testCase{
		// Happy paths
		{
			name:      "valid task operation with RunHook",
			args:      validTaskOperationArgs(),
			isChild:   false,
			expectErr: false,
		},
		{
			name: "valid task operation with children",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.RunHook = nil
				args.Children = []*OperationArgs{
					{
						ProjectName: "default",
						Type:        operationtype.InstanceStop,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.InstanceURL("default", "test-instance-1"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
					},
				}

				return args
			}(),
			isChild:   false,
			expectErr: false,
		},
		{
			name:      "valid websocket operation",
			args:      validWebsocketOperationArgs(),
			isChild:   false,
			expectErr: false,
		},
		{
			name:      "valid token operation",
			args:      validTokenOperationArgs(),
			isChild:   false,
			expectErr: false,
		},
		{
			name:      "valid server-level operation",
			args:      validServerLevelOperationArgs(),
			isChild:   false,
			expectErr: false,
		},
		{
			name: "valid operation with EntityURL matching operation type",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.NetworkUpdate
				args.EntityURL = entity.NetworkURL("default", "lxdbr0")
				return args
			}(),
			isChild:   false,
			expectErr: false,
		},
		// Unhappy paths - invalid Class and Type
		{
			name: "invalid Class value",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Class = 99
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Unknown operation class",
		},
		{
			name: "invalid Type value",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.Type(9999)
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Unknown operation type code",
		},
		// Unhappy paths - EntityURL validation
		{
			name: "EntityURL with wrong entity type",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceCreate
				args.EntityURL = entity.NetworkURL("default", "test-network")
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    `Entity type for URL "/1.0/networks/test-network?project=default" does not match operation entity type "project"`,
		},
		{
			name: "missing EntityURL for non-server operation",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceCreate
				args.EntityURL = nil
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Operation entity URL required",
		},
		{
			name: "invalid EntityURL format",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.EntityURL = api.NewURL().Path("invalid-url")
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Invalid operation entity URL",
		},
		// Unhappy paths - conflict reference
		{
			name: "conflict reference with action none",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.ConflictReference = "foo"
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    `Conflict reference "foo" provided for operation type "Creating instance" that does not support conflicts`,
		},
		// Unhappy paths - stage on parent
		{
			name: "stage on parent",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Stage = 1
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Only child operations have stages",
		},
		// Unhappy paths - nested bulk operations
		{
			name: "nested bulk operations",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Children = []*OperationArgs{
					{
						ProjectName: "default",
						Type:        operationtype.InstanceCreate,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.InstanceURL("default", "test-instance-1"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
					},
				}

				return args
			}(),
			isChild:   true,
			expectErr: true,
			errMsg:    `Child operations not allowed for operation type "Creating instance"`,
		},
		{
			name: "bulk operation without children",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.RunHook = nil
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Bulk operations must have children",
		},
		{
			name: "bulk operation as a child",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.RunHook = nil
				args.Children = []*OperationArgs{
					{
						ProjectName: "default",
						Type:        operationtype.InstanceStateUpdateBulk,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.ProjectURL("default"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
					},
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Bulk operations cannot have nested bulk operations",
		},
		// Unhappy paths - Task operation requirements
		{
			name: "task operation without RunHook and without children",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.RunHook = nil
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Task operations must have a Run hook",
		},
		// Unhappy paths - Websocket operation requirements
		{
			name: "websocket operation without ConnectHook",
			args: func() OperationArgs {
				args := validWebsocketOperationArgs()
				args.ConnectHook = nil
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Websocket operations must have a Connect hook",
		},
		// Unhappy paths - Token operation requirements
		{
			name: "token operation with RunHook",
			args: func() OperationArgs {
				args := validTokenOperationArgs()
				args.RunHook = func(ctx context.Context, op *Operation) error {
					return nil
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Token operations cannot have a Run hook",
		},
		// Unhappy paths - hook restrictions
		{
			name: "non-websocket operation with ConnectHook",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.ConnectHook = func(op *Operation, r *http.Request, w http.ResponseWriter) error {
					return nil
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Only websocket operations can have a Connect hook",
		},
		// Unhappy paths - children restrictions
		{
			name: "non-task operation with children",
			args: func() OperationArgs {
				args := validWebsocketOperationArgs()
				args.Children = []*OperationArgs{
					{
						ProjectName: "default",
						Type:        operationtype.InstanceCreate,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.InstanceURL("default", "test-instance-1"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
					},
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    `Child operations not allowed for operation type "Showing console"`,
		},
		// Unhappy paths - child operation restrictions
		{
			name: "non-task operation as child",
			args: func() OperationArgs {
				args := validWebsocketOperationArgs()
				return args
			}(),
			isChild:   true,
			expectErr: true,
			errMsg:    `Operations of class "websocket" cannot have a parent operation`,
		},
		{
			name: "nil child",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.Children = append(args.Children, nil)
				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Operation children cannot be nil",
		},
		{
			name: "child with different to parent",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.Children = []*OperationArgs{
					{
						ProjectName: "foo",
						Type:        operationtype.InstanceCreate,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.InstanceURL("foo", "test-instance-1"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
					},
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Child operations cannot have a different project to the parent operation",
		},
		{
			name: "children with invalid stages",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.Children = []*OperationArgs{
					{
						ProjectName: "default",
						Type:        operationtype.InstanceCreate,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.ProjectURL("default"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
						Stage: 1,
					},
					{
						ProjectName: "default",
						Type:        operationtype.InstanceCreate,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.ProjectURL("default"),
						RunHook: func(ctx context.Context, op *Operation) error {
							return nil
						},
						Stage: 5,
					},
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Child operation stages must be consecutive, starting at 0",
		},
		// Unhappy paths - child validation failure propagation
		{
			name: "child operation validation failure",
			args: func() OperationArgs {
				args := validTaskOperationArgs()
				args.Type = operationtype.InstanceStateUpdateBulk
				args.RunHook = nil
				args.Children = []*OperationArgs{
					{
						ProjectName: "default",
						Type:        operationtype.InstanceCreate,
						Class:       operationtype.OperationClassTask,
						EntityURL:   entity.InstanceURL("default", "test-instance-1"),
						RunHook:     nil,
					},
				}

				return args
			}(),
			isChild:   false,
			expectErr: true,
			errMsg:    "Failed validating child operation",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := tt.args.validate(tt.isChild)
			if tt.expectErr {
				s.Error(err)
				s.Contains(err.Error(), tt.errMsg)
			} else {
				s.NoError(err)
			}
		})
	}
}
