package supabase

import "testing"

func TestRejectsServiceRoleKey(t *testing.T) {
	// Header and payload only; the signature is irrelevant because the role is rejected first.
	serviceRole := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoic2VydmljZV9yb2xlIn0.sig"
	if _, err := New("https://example.supabase.co", serviceRole); err == nil {
		t.Fatal("service role key was accepted")
	}
	anon := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoiYW5vbiJ9.sig"
	client, err := New("https://example.supabase.co", anon)
	if err != nil {
		t.Fatal(err)
	}
	if client.anonKey != anon {
		t.Fatal("anon key was not preserved")
	}
}
