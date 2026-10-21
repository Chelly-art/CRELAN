package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type appPersistentStorage struct {
	Size         string `json:"size"`
	MountPath    string `json:"mount_path"`
	StorageClass string `json:"storage_class"`
	AccessMode   string `json:"access_mode"`
}

var storageClassNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)

func validateAppPersistentStorage(storage *appPersistentStorage) error {
	if storage == nil {
		return nil
	}
	storage.Size = strings.TrimSpace(storage.Size)
	storage.MountPath = strings.TrimSpace(storage.MountPath)
	storage.StorageClass = strings.TrimSpace(storage.StorageClass)
	storage.AccessMode = strings.TrimSpace(storage.AccessMode)
	if storage.AccessMode == "" {
		storage.AccessMode = "single_writer"
	}
	if storage.AccessMode != "single_writer" && storage.AccessMode != "shared" {
		return errors.New("persistent storage access_mode must be single_writer or shared")
	}
	if storage.Size == "" || storage.MountPath == "" {
		return errors.New("persistent storage needs a size and mount path")
	}
	quantity, err := resource.ParseQuantity(storage.Size)
	if err != nil || quantity.Sign() <= 0 {
		return errors.New("persistent storage size must be a positive Kubernetes quantity")
	}
	max, err := resource.ParseQuantity(configuredAppStorageMax())
	if err != nil || max.Sign() <= 0 {
		max = resource.MustParse("20Gi")
	}
	if quantity.Cmp(max) > 0 {
		return fmt.Errorf("persistent storage size exceeds the per-app maximum of %s", max.String())
	}
	if storage.StorageClass != "" && !storageClassNamePattern.MatchString(storage.StorageClass) {
		return errors.New("storage class is invalid")
	}
	mount := storage.MountPath
	if !path.IsAbs(mount) || path.Clean(mount) != mount || mount == "/" || strings.ContainsAny(mount, "\x00\r\n") || len(mount) > 240 {
		return errors.New("mount path must be a clean absolute path other than /")
	}
	for _, restricted := range []string{"/proc", "/sys", "/dev", "/etc"} {
		if mount == restricted || strings.HasPrefix(mount, restricted+"/") {
			return errors.New("mount path cannot replace a protected system directory")
		}
	}
	return nil
}

func resolveAppStorageClass(storage *appPersistentStorage) error {
	if storage == nil || storage.StorageClass != "" {
		return nil
	}
	allowed := allowedAppStorageClasses()
	if storage.AccessMode == "shared" {
		allowed = allowedSharedAppStorageClasses()
	}
	if len(allowed) != 1 {
		return errors.New("choose a storage class enabled by the platform for this access mode")
	}
	for name := range allowed {
		storage.StorageClass = name
	}
	return nil
}

func configuredAppStorageMax() string {
	value := strings.TrimSpace(getenv("CRELAN_APP_STORAGE_MAX_SIZE"))
	if value == "" {
		return "20Gi"
	}
	return value
}

func configuredWorkspaceStorageQuota() string {
	value := strings.TrimSpace(getenv("CRELAN_WORKSPACE_STORAGE_QUOTA"))
	if quantity, err := resource.ParseQuantity(value); err == nil && quantity.Sign() > 0 {
		return quantity.String()
	}
	return "2000Gi"
}

func configuredWorkspaceAppStorageQuota() resource.Quantity {
	value := strings.TrimSpace(getenv("CRELAN_WORKSPACE_APP_STORAGE_QUOTA"))
	if quantity, err := resource.ParseQuantity(value); err == nil && quantity.Sign() > 0 {
		return quantity
	}
	return resource.MustParse("100Gi")
}

func allowedAppStorageClasses() map[string]struct{} {
	return parseStorageClassAllowlist(getenv("CRELAN_ALLOWED_APP_STORAGE_CLASSES"))
}

func allowedSharedAppStorageClasses() map[string]struct{} {
	return parseStorageClassAllowlist(getenv("CRELAN_ALLOWED_APP_SHARED_STORAGE_CLASSES"))
}

func parseStorageClassAllowlist(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if storageClassNamePattern.MatchString(name) {
			result[name] = struct{}{}
		}
	}
	return result
}

func validateStorageClass(ctx context.Context, provisioner *kubernetesProvisioner, name, accessMode string) error {
	allowed := allowedAppStorageClasses()
	if accessMode == "shared" {
		allowed = allowedSharedAppStorageClasses()
	}
	if _, ok := allowed[name]; !ok {
		return errors.New("requested storage class is not enabled by the platform")
	}
	if _, err := provisioner.client.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return errors.New("requested storage class is not available in this cluster")
		}
		return fmt.Errorf("check storage class: %w", err)
	}
	return nil
}

func appDataPVCName(appName string) string {
	if len(appName) > 58 {
		appName = strings.TrimRight(appName[:58], "-")
	}
	return appName + "-data"
}

func (p *kubernetesProvisioner) ensureAppPersistentVolume(ctx context.Context, workspaceID int64, namespace, appName string, storage *appPersistentStorage) error {
	if storage == nil {
		return nil
	}
	if err := resolveAppStorageClass(storage); err != nil {
		return err
	}
	if err := validateAppPersistentStorage(storage); err != nil {
		return err
	}
	if err := validateStorageClass(ctx, p, storage.StorageClass, storage.AccessMode); err != nil {
		return err
	}
	if err := p.checkWorkspaceAppStorageBudget(ctx, workspaceID, namespace, appName, storage.Size); err != nil {
		return err
	}
	name := appDataPVCName(appName)
	labels := map[string]string{workspaceLabel: fmt.Sprint(workspaceID), applicationLabel: appName}
	claims := p.client.CoreV1().PersistentVolumeClaims(namespace)
	current, err := claims.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if !ownsWorkspace(current.Labels, workspaceID) || current.Labels[applicationLabel] != appName {
			return errors.New("persistent volume claim is not owned by this application")
		}
		if current.Spec.StorageClassName == nil || *current.Spec.StorageClassName != storage.StorageClass {
			return errors.New("an existing app volume cannot change storage class")
		}
		requestedAccessMode := corev1.ReadWriteOnce
		if storage.AccessMode == "shared" {
			requestedAccessMode = corev1.ReadWriteMany
		}
		modeMatches := false
		for _, existingMode := range current.Spec.AccessModes {
			if existingMode == requestedAccessMode {
				modeMatches = true
				break
			}
		}
		if !modeMatches {
			return errors.New("an existing app volume cannot change access mode")
		}
		oldSize := current.Spec.Resources.Requests[corev1.ResourceStorage]
		requested := resource.MustParse(storage.Size)
		if requested.Cmp(oldSize) != 0 {
			return errors.New("an existing app volume cannot be resized through deployment updates")
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("read app volume: %w", err)
	}
	quantity := resource.MustParse(storage.Size)
	accessMode := corev1.ReadWriteOnce
	if storage.AccessMode == "shared" {
		accessMode = corev1.ReadWriteMany
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels,
			Annotations: map[string]string{
				"crelan.io/storage-retention":   "retain-on-app-delete",
				"crelan.io/storage-mount-path":  storage.MountPath,
				"crelan.io/storage-access-mode": storage.AccessMode,
			}},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{accessMode},
			StorageClassName: &storage.StorageClass,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity}},
		},
	}
	if _, err := claims.Create(ctx, claim, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create app persistent volume claim: %w", err)
	}
	return nil
}

func (p *kubernetesProvisioner) checkWorkspaceAppStorageBudget(ctx context.Context, workspaceID int64, namespace, appName, requested string) error {
	claims, err := p.client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{LabelSelector: workspaceLabel + "=" + fmt.Sprint(workspaceID)})
	if err != nil {
		return fmt.Errorf("check workspace app storage budget: %w", err)
	}
	total := resource.MustParse("0")
	appVolumeFound := false
	for i := range claims.Items {
		claim := &claims.Items[i]
		if !ownsWorkspace(claim.Labels, workspaceID) || claim.Labels[applicationLabel] == "" {
			continue
		}
		quantity := claim.Spec.Resources.Requests[corev1.ResourceStorage]
		total.Add(quantity)
		if claim.Labels[applicationLabel] == appName && claim.Name == appDataPVCName(appName) {
			appVolumeFound = true
		}
	}
	if !appVolumeFound {
		total.Add(resource.MustParse(requested))
	}
	if total.Cmp(configuredWorkspaceAppStorageQuota()) > 0 {
		return errors.New("workspace application storage quota exceeded")
	}
	return nil
}

func (p *kubernetesProvisioner) deleteAppPersistentVolume(ctx context.Context, workspaceID int64, name string) error {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return err
	}
	claimName := appDataPVCName(name)
	claim, err := p.client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claimName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read app volume for deletion: %w", err)
	}
	if !ownsWorkspace(claim.Labels, workspaceID) || claim.Labels[applicationLabel] != name {
		return errors.New("persistent volume claim is not owned by this application")
	}
	if err := p.client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, claimName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete app persistent volume claim: %w", err)
	}
	return nil
}

func appConnectionURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("connection URL must be an absolute URL without embedded credentials or fragments")
	}
	return parsed.String(), nil
}
