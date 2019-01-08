# Change log

All notable changes to the LaunchDarkly git-flag-parser will be documented in this file. This project adheres to [Semantic Versioning](http://semver.org).

## [0.1.0] - 2019-01-02
### Changed
- `pushTime` CLI arg renamed to `updateSequenceId`. Its type has been changed from timestamp to integer.
  - Note: this is not considered a breaking change as the CLI args are still in flux. After the 1.0 release arg changes will be considered breaking.

### Fixed
- Upserting repos no longer fails on non-existent repos

## [0.0.1] - 2018-12-14
### Added
- Automated release pipeline for github releases and docker images
- Changelog