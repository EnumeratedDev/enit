# Enit
## The official init system for Tide Linux

### Project Information
Enit is an init system developed for the Tide Linux project. It features easy-to-use commands for power and service management and yaml-based service configuration

### Installation Guide
#### Using a package manager
- Tide Linux: Enit is pre-installed as the default init system through the `enit` package
#### Building from source
- Download `go` from your package manager or from the go website
- Download `make` from your package manager
- Run the following command to compile the project
```
make PREFIX=/usr SYSCONFDIR=/etc LOCALSTATEDIR=/var
```
- Run the following command to install Enit into your system. You may also append a DESTDIR variable at the end of this line if you wish to install in a different root directory
```
make install PREFIX=/usr
make install-config PREFIX=/usr SYSCONFDIR=/etc
```
### Post installation
- Set the default init system in your bootloader/boot-manager by appending `init=/usr/sbin/enit` to your kernel command-line parameters. Alternatively symlink `/usr/sbin/enit` to `/sbin/init`
- You may find additional service files in the [Tide Linux repositories](https://git.enumerated.dev/tide-linux). Some service files will likely need to be modified to run in your distribution
