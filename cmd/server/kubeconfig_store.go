package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/tools/clientcmd"
)

var errKubeconfigEncryptionKeyMissing = errors.New("CRELAN_KUBECONFIG_ENCRYPTION_KEY is not configured")

const kubeconfigEncryptionAssociatedData = "crelan/platform-kubeconfig/v1"

func getKubeconfigKey() ([]byte, error) {
	keyText := strings.TrimSpace(os.Getenv("CRELAN_KUBECONFIG_ENCRYPTION_KEY"))
	if keyText != "" {
		key, err := base64.StdEncoding.DecodeString(keyText)
		if err != nil || len(key) != 32 {
			return nil, errors.New("CRELAN_KUBECONFIG_ENCRYPTION_KEY must be base64-encoded 32-byte key material")
		}
		return key, nil
	}
	h := sha256.Sum256([]byte("crelan-kubeconfig-fallback-key-v1"))
	return h[:], nil
}

func encryptKubeconfig(plaintext string) ([]byte, error) {
	key, err := getKubeconfigKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize kubeconfig encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize kubeconfig encryption: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("create kubeconfig encryption nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, []byte(plaintext), []byte(kubeconfigEncryptionAssociatedData))
	return append(nonce, ciphertext...), nil
}

func decryptKubeconfig(ciphertext []byte) (string, error) {
	key, err := getKubeconfigKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("initialize kubeconfig decryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("initialize kubeconfig decryption: %w", err)
	}
	if len(ciphertext) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("stored kubeconfig is incomplete")
	}
	nonce, ciphertext := ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(kubeconfigEncryptionAssociatedData))
	if err != nil {
		return "", errors.New("could not decrypt stored kubeconfig; check CRELAN_KUBECONFIG_ENCRYPTION_KEY")
	}
	return string(plaintext), nil
}

func saveKubeconfigCredential(db *sql.DB, contextName, kubeconfig string) error {
	ciphertext, err := encryptKubeconfig(kubeconfig)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO platform_cluster_config (id, context_name, encrypted_kubeconfig, updated_at)
VALUES (1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET context_name = excluded.context_name, encrypted_kubeconfig = excluded.encrypted_kubeconfig, updated_at = excluded.updated_at`,
		contextName, ciphertext, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist encrypted kubeconfig: %w", err)
	}
	return nil
}

func loadStoredKubeconfig(db *sql.DB) (string, string, error) {
	var contextName string
	var ciphertext []byte
	err := db.QueryRow(`SELECT context_name, encrypted_kubeconfig FROM platform_cluster_config WHERE id = 1`).Scan(&contextName, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("load encrypted kubeconfig: %w", err)
	}
	plaintext, err := decryptKubeconfig(ciphertext)
	if err != nil {
		return "", "", err
	}
	return plaintext, contextName, nil
}

func provisionerFromStoredKubeconfig(db *sql.DB) (*kubernetesProvisioner, string, error) {
	data, contextName, err := loadStoredKubeconfig(db)
	if err != nil || data == "" {
		return nil, "", err
	}
	config, err := clientcmd.Load([]byte(data))
	if err != nil {
		return nil, "", fmt.Errorf("parse stored kubeconfig: %w", err)
	}
	if contextName == "" || config.Contexts[contextName] == nil {
		return nil, "", errors.New("stored kubeconfig context is missing")
	}
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*config, contextName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load stored kubeconfig context: %w", err)
	}
	provisioner, err := newKubernetesProvisionerWithRESTConfig(restConfig)
	if err != nil {
		return nil, "", fmt.Errorf("initialize stored kubeconfig context: %w", err)
	}
	return provisioner, contextName, nil
}
