# Publishing to GitHub

## 1. Create the repository

Create an empty GitHub repository named `WIFI-Watchdog` under the `Muelsysel` account. Do not pre-create README/License if you want to push this directory directly.

## 2. Initial push

```powershell
git init
git add .
git commit -m "feat: open-source WiFi Watchdog v1.3.0"
git branch -M main
git remote add origin https://github.com/Muelsysel/WIFI-Watchdog.git
git push -u origin main
```

## 3. Create the first release

The repository includes `.github/workflows/release.yml`. Push a semantic-version tag:

```powershell
git tag v1.3.0
git push origin v1.3.0
```

GitHub Actions will:

1. run tests and vet;
2. build Windows x64 and ARM64 GUI executables;
3. package README/LICENSE/CHANGELOG with each executable;
4. create SHA-256 checksums;
5. create a GitHub Release for the tag.

## 4. Recommended repository settings

- enable Issues;
- enable Discussions if you want community Q&A;
- protect `main` after the project has multiple contributors;
- require CI before merging pull requests;
- enable Dependabot alerts and GitHub Actions updates;
- add a real security contact when available.
