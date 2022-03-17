package dnsprovider

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_getEnvOptionalInt(t *testing.T) {
	type args struct {
		key          string
		defaultValue int
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{"use default values", args{key: "CLOUNDS_SOME_NOT_EXISTING_ENV", defaultValue: 55660}, 55660},
		{"use env value", args{key: "CLOUDNS_SOME_EXISTING_ENV", defaultValue: 87701}, 7007},
	}

	os.Setenv("CLOUDNS_SOME_EXISTING_ENV", "7007")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getEnvOptionalInt(tt.args.key, tt.args.defaultValue); got != tt.want {
				t.Errorf("getEnvOptionalInt() = %v, want %v", got, tt.want)
			}
		})
	}

	os.Setenv("CLOUNDS_BOGUS_ENV", "abc100")
	v := getEnvOptionalInt("CLOUNDS_BOGUS_ENV", 55660)
	assert.Equal(t, 55660, v, "bogus env")

}

func Test_getEnvOptionalString(t *testing.T) {
	type args struct {
		name         string
		defaultValue string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"use default values", args{name: "CLOUDNS_2_SOME_NOT_EXISTING_ENV", defaultValue: "id123"}, "id123"},
		{"use env value", args{name: "CLOUDNS_2SOME_EXISTING_ENV", defaultValue: "defval12345"}, "val12345"},
	}

	os.Setenv("CLOUDNS_2SOME_EXISTING_ENV", "val12345")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getEnvOptionalString(tt.args.name, tt.args.defaultValue); got != tt.want {
				t.Errorf("getEnvOptionalString() = %v, want %v", got, tt.want)
			}
		})
	}
}
