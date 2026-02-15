## alpacon login

Log in to Alpacon

### Synopsis

Log in to Alpacon. A workspace URL must be specified to access Alpacon.

```
alpacon login [flags]
```

### Examples

```
	# Re-login to saved workspace
	alpacon login

	# Cloud login (portal URL or API URL)
	alpacon login https://alpacon.io/myworkspace
	alpacon login myworkspace.us1.alpacon.io

	# Self-hosted
	alpacon login alpacon.example.com

	# Login via API Token
	alpacon login myworkspace.us1.alpacon.io -t [TOKEN_KEY]

	# Legacy username/password
	alpacon login [WORKSPACE_URL] -u [USERNAME] -p [PASSWORD]

	# Skip TLS certificate verification
	alpacon login [WORKSPACE_URL] --insecure
```

### Options

```
  -h, --help              help for login
      --insecure          Skip TLS certificate verification
  -p, --password string   Password for login
  -t, --token string      API token for login
  -u, --username string   Username for login
```

### SEE ALSO

* [alpacon](alpacon.md)	 - Alpacon CLI: Your Gateway to Alpacon Services
