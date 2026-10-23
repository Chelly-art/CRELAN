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
	"log"
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO platform_cluster_config (context_name, encrypted_kubeconfig, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(context_name) DO UPDATE SET encrypted_kubeconfig = excluded.encrypted_kubeconfig, updated_at = excluded.updated_at`,
		contextName, ciphertext, now)
	if err != nil {
		return fmt.Errorf("persist encrypted kubeconfig: %w", err)
	}
	return nil
}

func deleteKubeconfigCredential(db *sql.DB, contextName string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`DELETE FROM platform_cluster_config WHERE context_name = ?`, contextName)
	return err
}

func loadStoredKubeconfigByContext(db *sql.DB, targetContext string) (string, error) {
	if db == nil {
		return "", sql.ErrNoRows
	}
	var ciphertext []byte
	err := db.QueryRow(`SELECT encrypted_kubeconfig FROM platform_cluster_config WHERE context_name = ?`, targetContext).Scan(&ciphertext)
	if err != nil {
		return "", err
	}
	return decryptKubeconfig(ciphertext)
}

func loadAllStoredKubeconfigs(db *sql.DB) (map[string]string, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT context_name, encrypted_kubeconfig FROM platform_cluster_config ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query stored kubeconfigs: %w", err)
	}
	defer rows.Close()

	results := make(map[string]string)
	for rows.Next() {
		var ctxName string
		var ciphertext []byte
		if err := rows.Scan(&ctxName, &ciphertext); err != nil {
			return nil, err
		}
		plaintext, err := decryptKubeconfig(ciphertext)
		if err != nil {
			log.Printf("decrypt stored kubeconfig for %q: %v", ctxName, err)
			continue
		}
		results[ctxName] = plaintext
	}
	return results, nil
}

func loadStoredKubeconfig(db *sql.DB) (string, string, error) {
	if db == nil {
		return "", "", nil
	}
	var contextName string
	var ciphertext []byte
	err := db.QueryRow(`SELECT context_name, encrypted_kubeconfig FROM platform_cluster_config ORDER BY updated_at DESC LIMIT 1`).Scan(&contextName, &ciphertext)
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

func provisionerFromKubeconfigYAML(yamlContent, contextName string) (*kubernetesProvisioner, error) {
	config, err := clientcmd.Load([]byte(yamlContent))
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	if contextName == "" {
		contextName = config.CurrentContext
	}
	if contextName == "" && len(config.Contexts) == 1 {
		for name := range config.Contexts {
			contextName = name
		}
	}
	if contextName == "" || config.Contexts[contextName] == nil {
		return nil, fmt.Errorf("context %q not found in kubeconfig", contextName)
	}
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*config, contextName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	restConfig.Timeout = 8 * time.Second
	return newKubernetesProvisionerWithRESTConfig(restConfig)
}

func allProvisionersFromStoredKubeconfigs(db *sql.DB) (map[string]*kubernetesProvisioner, string, error) {
	stored, err := loadAllStoredKubeconfigs(db)
	if err != nil {
		return nil, "", err
	}
	if len(stored) == 0 {
		return nil, "", nil
	}
	results := make(map[string]*kubernetesProvisioner)
	var latestContext string
	_ = db.QueryRow(`SELECT context_name FROM platform_cluster_config ORDER BY updated_at DESC LIMIT 1`).Scan(&latestContext)

	for ctxName, yamlContent := range stored {
		prov, err := provisionerFromKubeconfigYAML(yamlContent, ctxName)
		if err != nil {
			log.Printf("initialize stored provisioner for %q: %v", ctxName, err)
			continue
		}
		results[ctxName] = prov
	}
	return results, latestContext, nil
}

func provisionerFromStoredKubeconfig(db *sql.DB) (*kubernetesProvisioner, string, error) {
	data, contextName, err := loadStoredKubeconfig(db)
	if err != nil || data == "" {
		return nil, "", err
	}
	prov, err := provisionerFromKubeconfigYAML(data, contextName)
	return prov, contextName, err
}

