package selfupdate

import "fmt"

func ArchiveName(version, goos, goarch string) string { // Mirrors the archives name_template in .goreleaser.yaml.
	extension := "tar.gz"
	if goos == "windows" {
		extension = "zip"
	}
	return fmt.Sprintf("alpacon-%s-%s-%s.%s", version, goos, goarch, extension)
}

func BinaryName(goos string) string { // Fixed by the build, so a binary renamed on disk—go install names it after the module, alpacon-cli—still finds its own file inside.
	if goos == "windows" {
		return "alpacon.exe"
	}
	return "alpacon"
}

func ChecksumsName(version string) string {
	return fmt.Sprintf("alpacon-%s-checksums.sha256", version)
}

func SelectAsset(release *Release, name string) (Asset, error) {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return asset, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s carries no asset named %s", release.Version, name)
}
