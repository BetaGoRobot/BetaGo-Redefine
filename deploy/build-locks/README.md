# Existing production module locks

`master.mod/.sum` freeze the deployed MASTER reaction-observability runtime's Go 1.26.8 dependencies. `next.mod/.sum` freeze the independent next network/cover runtime's Go 1.26.0 dependencies. These contain module coordinates and checksums only, never service configuration.

The repository root combines the two source trees for development. Narrow production releases retain each runtime's existing dependencies instead of introducing the other runtime's library upgrades. Use the declared toolchain with `-modfile=deploy/build-locks/master.mod` or `-modfile=deploy/build-locks/next.mod`, `-mod=readonly`, and `-tags=custom_skip_vips`. Tests also use `-v` and the existing main workspace's absolute `BETAGO_CONFIG_PATH`; never copy the private configuration.

The release source manifests record the actual module file hashes and their matching lock files. MASTER's GoJieba build must keep its native source paths, without `-trimpath`, so its runtime dictionaries remain discoverable. Next retains its existing CGO-disabled build.
