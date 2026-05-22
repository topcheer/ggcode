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

### ios fetch_profiles

```sh
[bundle exec] fastlane ios fetch_profiles
```

Fetch provisioning profiles for CI (sigh)

### ios upload_metadata

```sh
[bundle exec] fastlane ios upload_metadata
```

Upload metadata and screenshots (run once)

### ios upload_ipa

```sh
[bundle exec] fastlane ios upload_ipa
```

Upload pre-built IPA to TestFlight

### ios upload_testflight

```sh
[bundle exec] fastlane ios upload_testflight
```

Upload to TestFlight (uses flutter-built IPA if available)

### ios promote_external

```sh
[bundle exec] fastlane ios promote_external
```

Wait for TestFlight processing, distribute to External Testing

### ios deploy_external

```sh
[bundle exec] fastlane ios deploy_external
```

Full deploy: upload → wait for processing → External Testing → submit for review if needed

### ios submit

```sh
[bundle exec] fastlane ios submit
```

Submit latest build for App Store Review

### ios inspect

```sh
[bundle exec] fastlane ios inspect
```

Show App Store Connect state

----

This README.md is auto-generated and will be re-generated every time [_fastlane_](https://fastlane.tools) is run.

More information about _fastlane_ can be found on [fastlane.tools](https://fastlane.tools).

The documentation of _fastlane_ can be found on [docs.fastlane.tools](https://docs.fastlane.tools).
