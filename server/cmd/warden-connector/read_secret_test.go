package main

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestReadSecretTokenReadsFromSecret(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "jenkins-cred", Namespace: "warden-system"},
		Data:       map[string][]byte{"token": []byte("the-secret-token")},
	})
	resolver := readSecretToken(client, "warden-system")
	value, err := resolver(context.Background(), "jenkins-cred", "")
	if err != nil {
		t.Fatalf("resolve secret token: %v", err)
	}
	if value != "the-secret-token" {
		t.Fatalf("token = %q", value)
	}
}

func TestReadSecretTokenErrorsOnMissingSecretOrKey(t *testing.T) {
	client := fake.NewSimpleClientset()
	resolver := readSecretToken(client, "warden-system")
	if _, err := resolver(context.Background(), "missing", ""); err == nil {
		t.Fatal("missing secret should error")
	}

	client = fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "no-token", Namespace: "warden-system"},
		Data:       map[string][]byte{"password": []byte("x")},
	})
	resolver = readSecretToken(client, "warden-system")
	_, err := resolver(context.Background(), "no-token", "")
	if err == nil || !strings.Contains(err.Error(), "no token key") {
		t.Fatalf("secret without token key error = %v", err)
	}
}
