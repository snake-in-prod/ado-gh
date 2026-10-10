package auth

import "testing"

type mapConfig map[string]string

func (m mapConfig) Get(_, key string) string { return m[key] }

func TestEntraClientID(t *testing.T) {
	tests := []struct {
		name    string
		def     string
		env     string
		cfg     ConfigGetter
		want    string
		wantErr bool
	}{
		{name: "env wins", def: "default", env: "from-env", cfg: mapConfig{"entra_client_id": "from-config"}, want: "from-env"},
		{name: "config over default", def: "default", cfg: mapConfig{"entra_client_id": "from-config"}, want: "from-config"},
		{name: "default", def: "default", cfg: mapConfig{}, want: "default"},
		{name: "nil config uses default", def: "default", want: "default"},
		{name: "nothing set", cfg: mapConfig{}, wantErr: true},
		{name: "nothing set, nil config", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := DefaultEntraClientID
			DefaultEntraClientID = tt.def
			t.Cleanup(func() { DefaultEntraClientID = old })
			t.Setenv("ADO_ENTRA_CLIENT_ID", tt.env)
			got, err := EntraClientID(tt.cfg)
			if tt.wantErr {
				want := "Entra ID login needs a client ID: set ADO_ENTRA_CLIENT_ID (or `ado config set entra_client_id <id>`), or log in with a PAT"
				if err == nil || err.Error() != want {
					t.Fatalf("err = %v, want %q", err, want)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("EntraClientID() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
