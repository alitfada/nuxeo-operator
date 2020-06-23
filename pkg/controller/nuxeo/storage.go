package nuxeo

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"nuxeo-operator/pkg/apis/nuxeo/v1alpha1"
	"nuxeo-operator/pkg/util"
)

// handleStorage supports the ability to define persistent storage for certain types of Nuxeo storage. For example,
// Nuxeo stores document attachments as binary blobs on the file system. This function and the underlying configuration
// structures allow these blobs to be stored persistently.
func handleStorage(dep *appsv1.Deployment, nodeSet v1alpha1.NodeSet) error {
	for _, storage := range nodeSet.Storage {
		if volume, err := createVolumeForStorage(storage); err != nil {
			return err
		} else {
			volMnt := createVolumeMountForStorage(storage.StorageType, volume.Name)
			envVar := createEnvVarForStorage(storage.StorageType, volMnt.MountPath)
			dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes, volume)
			if nuxeoContainer, err := util.GetNuxeoContainer(dep); err != nil {
				return err
			} else {
				nuxeoContainer.VolumeMounts = append(nuxeoContainer.VolumeMounts, volMnt)
				if envVar != (corev1.EnvVar{}) {
					nuxeoContainer.Env = append(nuxeoContainer.Env, envVar)
				}
			}
		}
	}
	return nil
}

// createEnvVarForStorage creates environment variables defining directory names for some storage types. This
// allows Nuxeo to use a different directory name than its default. Presently this is only supported for
// binaries and transient stores - and - requires a corresponding nuxeo.conf entry. (The nuxeo.conf entry is
// included in the default nuxeo.conf in the Nuxeo image.) Caller must add the variable to the Pod Spec. An
// empty EnvVar struct is returned if no environment variable mapping is valid for the passed storage type.
func createEnvVarForStorage(storageType v1alpha1.NuxeoStorage, mountPath string) corev1.EnvVar {
	switch storageType {
	case v1alpha1.NuxeoStorageBinaries:
		return corev1.EnvVar{
			Name:  "NUXEO_BINARY_STORE",
			Value: mountPath,
		}
	case v1alpha1.NuxeoStorageTransientStore:
		return corev1.EnvVar{
			Name:  "NUXEO_TRANSIENT_STORE",
			Value: mountPath,
		}
	case v1alpha1.NuxeoStorageConnect:
		fallthrough
	case v1alpha1.NuxeoStorageData:
		fallthrough
	case v1alpha1.NuxeoStorageNuxeoTmp:
		fallthrough
	default:
		return corev1.EnvVar{}
	}
}

// createVolumeMountForStorage creates and returns a VolumeMount struct for the passed storage and volume. Caller
// must add the struct to the Deployment.
func createVolumeMountForStorage(storageType v1alpha1.NuxeoStorage, volumeName string) corev1.VolumeMount {
	mountPath := getPathForStorageType(storageType)
	// todo-me I think this probably gets removed from the spec
	//if storage.MountPath != "" {
	//	mountPath = storage.MountPath
	//}
	volMnt := corev1.VolumeMount{
		Name:      volumeName,
		ReadOnly:  false,
		MountPath: mountPath,
	}
	return volMnt
}

// getPathForStorageType returns a Nuxeo-standard filesystem path for the passed storage type. E.g. 'NuxeoStorageBinaries'
// go in /var/lib/nuxeo/binaries, etc.
func getPathForStorageType(storageType v1alpha1.NuxeoStorage) string {
	switch storageType {
	case v1alpha1.NuxeoStorageBinaries:
		return "/var/lib/nuxeo/binaries"
	case v1alpha1.NuxeoStorageTransientStore:
		return "/var/lib/nuxeo/transientstore"
	case v1alpha1.NuxeoStorageConnect:
		return "/opt/nuxeo/connect"
	case v1alpha1.NuxeoStorageData:
		return "/var/lib/nuxeo/data"
	default:
		fallthrough
	case v1alpha1.NuxeoStorageNuxeoTmp:
		return "/opt/nuxeo/server/tmp"
	}
}

// createVolumeForStorage generates and returns a Volume struct for the passed storage. If the passed storage
// defines an explicit PVC template, then that is used. Else if the VolumeSource in the passed storage is
// explicitly defined, then that is used. Otherwise, a PVC volume source is generated with
// hard-coded defaults, based on the storage type. Caller must add the Volume to the Pod Spec.
func createVolumeForStorage(storage v1alpha1.NuxeoStorageSpec) (corev1.Volume, error) {
	volName := volumeNameForStorage(storage.StorageType)
	var volSrc corev1.VolumeSource
	if different, err := util.ObjectsDiffer(storage.VolumeClaimTemplate, corev1.PersistentVolumeClaim{}); err == nil && different {
		// explicit PVC template
		volSrc = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: storage.VolumeClaimTemplate.Name,
				ReadOnly:  false,
			},
		}
	} else if err != nil {
		return corev1.Volume{}, err
	} else if storage.VolumeSource != (corev1.VolumeSource{}) {
		// explicit volume source
		volSrc = storage.VolumeSource
	} else {
		// default: create a PVC volume source and generate the pvc name from the volume name
		volSrc = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: volName + "-pvc",
				ReadOnly:  false,
			},
		}
	}
	vol := corev1.Volume{
		Name:         volName,
		VolumeSource: volSrc,
	}
	return vol, nil
}

func volumeNameForStorage(storageType v1alpha1.NuxeoStorage) string {
	switch storageType {
	case v1alpha1.NuxeoStorageBinaries:
		return "binaries"
	case v1alpha1.NuxeoStorageTransientStore:
		return "transientstore"
	case v1alpha1.NuxeoStorageConnect:
		return "connect"
	case v1alpha1.NuxeoStorageData:
		return "data"
	default:
		fallthrough
	case v1alpha1.NuxeoStorageNuxeoTmp:
		return "nuxeotmp"
	}
}