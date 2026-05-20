fastlane documentation
----

# Installation

Make sure you have the latest version of the Xcode command line tools installed:

```sh
xcode-select --install
```

For _fastlane_ installation instructions, see [Installing _fastlane_](https://docs.fastlane.tools/#installing-fastlane)

# Available Actions

## iOS

### ios upload_metadata

```sh
[bundle exec] fastlane ios upload_metadata
```

Upload metadata to App Store Connect (no build)

### ios provision

```sh
[bundle exec] fastlane ios provision
```

Generate provisioning profiles via sigh

### ios build

```sh
[bundle exec] fastlane ios build
```

Build iOS Release Archive

### ios upload_testflight

```sh
[bundle exec] fastlane ios upload_testflight
```

Upload to TestFlight (uses flutter-built IPA if available)

### ios upload_ipa

```sh
[bundle exec] fastlane ios upload_ipa
```

Upload pre-built IPA to TestFlight (no build step)

### ios promote_external

```sh
[bundle exec] fastlane ios promote_external
```

Wait for TestFlight build processing, then distribute to External Testing

### ios deploy_external

```sh
[bundle exec] fastlane ios deploy_external
```

Full deploy: upload TestFlight + promote to External Testing

### ios release_latest

```sh
[bundle exec] fastlane ios release_latest
```

Submit latest TestFlight build for App Store Review (no rebuild)

### ios inspect

```sh
[bundle exec] fastlane ios inspect
```

Show all versions and builds on App Store Connect

### ios cleanup_live

```sh
[bundle exec] fastlane ios cleanup_live
```

Remove invalid 'live' version from App Store Connect

### ios full_release

```sh
[bundle exec] fastlane ios full_release
```

Full setup: upload metadata+screenshots, set review notes, select build, submit

### ios release

```sh
[bundle exec] fastlane ios release
```

Build and submit for App Store Review

----

This README.md is auto-generated and will be re-generated every time [_fastlane_](https://fastlane.tools) is run.

More information about _fastlane_ can be found on [fastlane.tools](https://fastlane.tools).

The documentation of _fastlane_ can be found on [docs.fastlane.tools](https://docs.fastlane.tools).
