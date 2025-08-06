package cluster

import (
	"context"
	"database/sql/driver"
	"fmt"
	"github.com/canonical/lxd/shared/entity"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestAuthSecretValue_Scan(t *testing.T) {
	type args struct {
		value any
	}
	tests := []struct {
		name    string
		s       AuthSecretValue
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, tt.s.Scan(tt.args.value), fmt.Sprintf("Scan(%v)", tt.args.value))
		})
	}
}

func TestAuthSecretValue_ScanText(t *testing.T) {
	type args struct {
		str string
	}
	tests := []struct {
		name    string
		s       AuthSecretValue
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, tt.s.ScanText(tt.args.str), fmt.Sprintf("ScanText(%v)", tt.args.str))
		})
	}
}

func TestAuthSecretValue_String(t *testing.T) {
	tests := []struct {
		name string
		s    AuthSecretValue
		want string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.s.String(), "String()")
		})
	}
}

func TestAuthSecretValue_Validate(t *testing.T) {
	tests := []struct {
		name    string
		s       AuthSecretValue
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, tt.s.Validate(), fmt.Sprintf("Validate()"))
		})
	}
}

func TestAuthSecretValue_Value(t *testing.T) {
	tests := []struct {
		name    string
		s       AuthSecretValue
		want    driver.Value
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.s.Value()
			if !tt.wantErr(t, err, fmt.Sprintf("Value()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "Value()")
		})
	}
}

func TestAuthSecret_Validate(t *testing.T) {
	type fields struct {
		ID           int
		Value        AuthSecretValue
		CreationDate time.Time
	}
	type args struct {
		expiry string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := AuthSecret{
				ID:           tt.fields.ID,
				Value:        tt.fields.Value,
				CreationDate: tt.fields.CreationDate,
			}
			tt.wantErr(t, s.Validate(tt.args.expiry), fmt.Sprintf("Validate(%v)", tt.args.expiry))
		})
	}
}

func TestAuthSecrets_Rotate(t *testing.T) {
	type args struct {
		ctx context.Context
		tx  *sql.Tx
	}
	tests := []struct {
		name    string
		s       AuthSecrets
		args    args
		want    AuthSecrets
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.s.Rotate(tt.args.ctx, tt.args.tx)
			if !tt.wantErr(t, err, fmt.Sprintf("Rotate(%v, %v)", tt.args.ctx, tt.args.tx)) {
				return
			}
			assert.Equalf(t, tt.want, got, "Rotate(%v, %v)", tt.args.ctx, tt.args.tx)
		})
	}
}

func TestAuthSecrets_Validate(t *testing.T) {
	type args struct {
		expiry string
	}
	tests := []struct {
		name    string
		s       AuthSecrets
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, tt.s.Validate(tt.args.expiry), fmt.Sprintf("Validate(%v)", tt.args.expiry))
		})
	}
}

func TestGetCoreAuthSecrets(t *testing.T) {
	type args struct {
		ctx context.Context
		tx  *sql.Tx
	}
	tests := []struct {
		name    string
		args    args
		want    AuthSecrets
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetCoreAuthSecrets(tt.args.ctx, tt.args.tx)
			if !tt.wantErr(t, err, fmt.Sprintf("GetCoreAuthSecrets(%v, %v)", tt.args.ctx, tt.args.tx)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetCoreAuthSecrets(%v, %v)", tt.args.ctx, tt.args.tx)
		})
	}
}

func TestSecretType_Scan(t *testing.T) {
	type args struct {
		value any
	}
	tests := []struct {
		name    string
		s       SecretType
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, tt.s.Scan(tt.args.value), fmt.Sprintf("Scan(%v)", tt.args.value))
		})
	}
}

func TestSecretType_ScanInteger(t *testing.T) {
	type args struct {
		code int64
	}
	tests := []struct {
		name    string
		s       SecretType
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, tt.s.ScanInteger(tt.args.code), fmt.Sprintf("ScanInteger(%v)", tt.args.code))
		})
	}
}

func TestSecretType_Value(t *testing.T) {
	tests := []struct {
		name    string
		s       SecretType
		want    driver.Value
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.s.Value()
			if !tt.wantErr(t, err, fmt.Sprintf("Value()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "Value()")
		})
	}
}

func Test_createCoreAuthSecret(t *testing.T) {
	type args struct {
		ctx    context.Context
		tx     *sql.Tx
		secret AuthSecret
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, createCoreAuthSecret(tt.args.ctx, tt.args.tx, tt.args.secret), fmt.Sprintf("createCoreAuthSecret(%v, %v, %v)", tt.args.ctx, tt.args.tx, tt.args.secret))
		})
	}
}

func Test_createSecret(t *testing.T) {
	type args struct {
		ctx        context.Context
		tx         *sql.Tx
		entityType entity.Type
		entityID   int
		secretType SecretType
		value      any
		createdAt  time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, createSecret(tt.args.ctx, tt.args.tx, tt.args.entityType, tt.args.entityID, tt.args.secretType, tt.args.value, tt.args.createdAt), fmt.Sprintf("createSecret(%v, %v, %v, %v, %v, %v, %v)", tt.args.ctx, tt.args.tx, tt.args.entityType, tt.args.entityID, tt.args.secretType, tt.args.value, tt.args.createdAt))
		})
	}
}

func Test_deleteSecretsByID(t *testing.T) {
	type args struct {
		ctx context.Context
		tx  *sql.Tx
		ids []int
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, deleteSecretsByID(tt.args.ctx, tt.args.tx, tt.args.ids...), fmt.Sprintf("deleteSecretsByID(%v, %v, %v)", tt.args.ctx, tt.args.tx, tt.args.ids...))
		})
	}
}

func Test_newAuthSecret(t *testing.T) {
	tests := []struct {
		name string
		want AuthSecret
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, newAuthSecret(), "newAuthSecret()")
		})
	}
}

func Test_newAuthSecretValue(t *testing.T) {
	tests := []struct {
		name string
		want AuthSecretValue
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, newAuthSecretValue(), "newAuthSecretValue()")
		})
	}
}