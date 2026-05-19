fastlane documentation
----

# Installation

Make sure you have the latest version of the Xcode command line tools installed:

```sh
xcode-select --install
```

For _fastlane_ installation instructions, see [Installing _fastlane_](https://docs.fastlane.tools/#installing-fastlane)

# Available Actions

## Android

### android build_aab

```sh
[bundle exec] fastlane android build_aab
```

Build release AAB

### android build_apk

```sh
[bundle exec] fastlane android build_apk
```

Build release APK

### android upload_metadata

```sh
[bundle exec] fastlane android upload_metadata
```

Upload metadata + images only (no AAB)

### android upload_internal

```sh
[bundle exec] fastlane android upload_internal
```

Upload AAB to Internal Testing

### android promote_alpha

```sh
[bundle exec] fastlane android promote_alpha
```

Promote latest from internal to alpha (Closed Testing)

### android promote_production

```sh
[bundle exec] fastlane android promote_production
```

Promote latest from alpha to production

### android deploy

```sh
[bundle exec] fastlane android deploy
```

Build → Upload to Internal Testing → Promote to Closed Testing

### android deploy_internal

```sh
[bundle exec] fastlane android deploy_internal
```

Build → Upload to Internal Testing only (no promote)

----

This README.md is auto-generated and will be re-generated every time [_fastlane_](https://fastlane.tools) is run.

More information about _fastlane_ can be found on [fastlane.tools](https://fastlane.tools).

The documentation of _fastlane_ can be found on [docs.fastlane.tools](https://docs.fastlane.tools).
