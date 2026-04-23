
# 01-swap-enable.yaml

Deploy services to automatically online sap if present in one or more of the following places:

- `/var/tmp/ocpswap.file` - A file which has to be created manually using `dd` and formated as swap using `mkswap`
- `PARTLABLE=OCPSWAP` - A partition which has to be created manually. Ideally on a disk with GPT parition, it must be labeled with `OCPSWAP` and must be formatted using `mkswap`


# 04-psi-enable.yaml

Enables PSI on all workers nodes as required by the descheduler and for performance analysis.
