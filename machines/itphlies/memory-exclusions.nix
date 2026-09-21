# itphlies: physical RAM pages reported by EDAC on 2026-09-21.
# Controller MC1, csrow 0, channel 8; firmware DIMM locator P1ML0.
# These 17 pages reserve 68 KiB before the kernel can allocate them.
#
# EDAC reports page-frame numbers. The byte addresses below are PFN * 4096,
# not the raw AMD machine-check Error Addr (which requires address translation).
# Keep this list with this host's current memory layout. Review/remove it after
# replacing the DIMM or changing firmware/memory layout.
#
# A NixOS rebuild installs these parameters for the next boot; switching the
# running system alone does not retire pages already in use.
# https://docs.kernel.org/admin-guide/kernel-parameters.html (memmap=nn$ss)
{config, ...}: let
  physicalPages = [
    "0xf5285c9000"
    "0xf5285d5000"
    "0xf5285e1000"
    "0xf5285e9000"
    "0xf5285ed000"
    "0xf5285f5000"
    "0xf528605000"
    "0xf52860d000"
    "0xf528611000"
    "0xf528625000"
    "0xf528629000"
    "0xf528631000"
    "0xf528649000"
    "0xf528655000"
    "0xf528659000"
    "0xf528665000"
    "0xf52867d000"
  ];
in {
  assertions = [
    {
      assertion = config.networking.hostName == "itphlies";
      message = "The P1ML0 memory exclusions apply only to itphlies.";
    }
  ];

  # '$' is literal here and in this host's systemd-boot options line.
  # Reserve only the recorded pages; preserve the firmware's remaining map.
  boot.kernelParams = builtins.map (address: "memmap=4K$" + address) physicalPages;
}
