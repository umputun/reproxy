# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2025-08-02
### Added
  - Support for libdns v1

## [1.0.3] - 2023-01-09
### Fixed
  - GetRecords now retrieves multiple pages of records automatically - if the total record count exceeds GoDaddy's per-API-call max of 500

## [1.0.2] - 2023-01-02
### Fixed
  - TTL settings are now properly updated in GoDaddy during AppendRecords() and SetRecords() calls

## [1.0.1] - 2022-11-13
### Changed
  - DeleteRecords() method now deletes records individually across multiple API calls for safety

## [1.0.0] - 2022-01-26
### Added
  - initial commit