# TASK: plugin-kasa

## Status: Functional

---

## go.mod

### 1. `replace` Directive Still Present (Fixed)
Redundant `replace` directive removed. Local resolution now relies on workspace `go.work`.

- [x] Remove the `replace` directive and rely on the workspace for local resolution

### 2. `sdk-entities` in Wrong require Block (Fixed)
`sdk-entities` moved to the direct require block.

- [x] Run `go mod tidy` to move it to the direct block and clean up stale entries

---

## Code

### 3. TCP I/O Blocking Inside Write Lock in `OnDevicesList` (Fixed)
`OnDevicesList` now snapshots the IP map and performs TCP `GetSysInfo` calls outside the lock.

- [x] Collect TCP results outside the lock

### 4. `OnEntitiesList` Early-Exit Prevents Entity Updates (Fixed)
The early-exit pattern has been replaced with a merge-by-ID pattern, allowing for entity updates.

- [x] Replace the early-exit with a merge-by-ID pattern

### 5. Multi-Outlet Child Devices Broken End-to-End (Fixed)
- Child outlets are now registered as individual devices in `OnDevicesList`.
- Child entities are correctly created in `OnEntitiesList`.
- `OnCommand` correctly identifies and routes commands to child outlets using the `-` separator in `SourceID`.

- [x] Create a device record per child outlet in `OnDevicesList`
- [x] Create corresponding entities per outlet in `OnEntitiesList`
- [x] Fix `OnCommand` child detection

### 6. `rgbToHsv` Silently Drops Brightness on SetRGB Commands (Fixed)
SetRGB commands now include the brightness value in the `SetLightState` payload.

- [x] Include brightness in the `SetLightState` payload

### 7. `defer stop()` in `discoveryLoop` Never Executes (Fixed)
Added clean loop exit using `context.Context`. The UDP listener is now explicitly closed when the context is cancelled.

- [x] Accept a `context.Context` in both goroutines and select on `ctx.Done()`
- [x] Call `stop()` explicitly when the context is cancelled

### 8. `OnStorageUpdate` Is a No-Op — IP Map Not Persisted (Fixed)
The IP map is now serialized to `Storage.Data` in `OnStorageUpdate` and restored in `OnInitialize`.

- [x] Serialize `p.ipMap` into `Storage.Data` in `OnStorageUpdate` and restore it in `OnInitialize`

### 9. Single TCP `Read` Fragile for Large Responses (Fixed)
`GetSysInfo` now reads the 4-byte length header first and uses `io.ReadFull` to ensure the entire payload is received.

- [x] Read the 4-byte length header first, then read exactly that many bytes