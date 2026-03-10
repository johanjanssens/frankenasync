export MAKEFLAGS='--silent --environment-override'

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

.ONESHELL:

.PHONY: build
build:
	if [ ! -f $(ROOT)/env.yaml ]; then
		echo "Error: env.yaml not found."
		echo "Run 'make php' to build PHP, then 'make env' to generate env.yaml."
		echo "See README.md for details."
		exit 1
	fi

	cd $(ROOT)
	CGO_ENABLED=1 go build -tags nowatcher -o dist/frankenasync .
	echo "Built dist/frankenasync"

.PHONY: run
run: build
	cd $(ROOT) && ./dist/frankenasync

.PHONY: test
test:
	cd $(ROOT) && go test ./asynctask/

.PHONY: bench
bench: build
	cd $(ROOT) && ./bench.sh

.PHONY: clean
clean:
	rm -rf dist/frankenasync

.PHONY: tidy
tidy:
	cd $(ROOT) && go mod tidy

# WordPress install for local dev/testing
WP_DIR := $(ROOT)/wordpress
WP_DB  := $(WP_DIR)/wp-content/database/.ht.sqlite

.PHONY: wordpress
wordpress:
	@if [ -f $(WP_DIR)/wp-config.php ]; then
		echo "WordPress already installed at $(WP_DIR)"
		echo "Run 'make wordpress-run' to start, or 'make wordpress-clean' to reinstall."
		exit 0
	fi

	echo "==> Downloading WordPress..."
	curl -sL https://wordpress.org/latest.tar.gz | tar xz -C $(ROOT)

	echo "==> Installing SQLite integration plugin..."
	mkdir -p $(WP_DIR)/wp-content/mu-plugins
	curl -sL https://downloads.wordpress.org/plugin/sqlite-database-integration.zip -o /tmp/sqlite-db.zip
	unzip -qo /tmp/sqlite-db.zip -d $(WP_DIR)/wp-content/mu-plugins
	rm /tmp/sqlite-db.zip
	cp $(WP_DIR)/wp-content/mu-plugins/sqlite-database-integration/db.copy $(WP_DIR)/wp-content/db.php

	echo "==> Creating wp-config.php..."
	cp $(WP_DIR)/wp-config-sample.php $(WP_DIR)/wp-config.php
	# Point DB_DIR to SQLite location
	sed -i '' "s|define( 'DB_NAME'.*|define( 'DB_DIR', '$(WP_DIR)/wp-content/database' );|" $(WP_DIR)/wp-config.php
	sed -i '' "s|define( 'DB_USER'.*|define( 'DB_FILE', '.ht.sqlite' );|" $(WP_DIR)/wp-config.php
	sed -i '' "s|define( 'DB_PASSWORD'.*|// removed — using SQLite|" $(WP_DIR)/wp-config.php
	sed -i '' "s|define( 'DB_HOST'.*|// removed — using SQLite|" $(WP_DIR)/wp-config.php
	# Generate salts
	curl -sL https://api.wordpress.org/secret-key/1.1/salt/ > /tmp/wp-salts.txt
	sed -i '' '/AUTH_KEY/d;/SECURE_AUTH_KEY/d;/LOGGED_IN_KEY/d;/NONCE_KEY/d' $(WP_DIR)/wp-config.php
	sed -i '' '/AUTH_SALT/d;/SECURE_AUTH_SALT/d;/LOGGED_IN_SALT/d;/NONCE_SALT/d' $(WP_DIR)/wp-config.php
	sed -i '' '/@-*[[:space:]]*unique/r /tmp/wp-salts.txt' $(WP_DIR)/wp-config.php
	rm /tmp/wp-salts.txt
	mkdir -p $(WP_DIR)/wp-content/database

	echo "==> WordPress installed at $(WP_DIR)"
	echo "    Run 'make wordpress-run' to start the server."
	echo "    Then visit http://localhost:8081 to complete setup."

.PHONY: wordpress-run
wordpress-run: build
	cd $(ROOT) && FRANKENASYNC_DOCROOT=$(WP_DIR) ./dist/frankenasync

.PHONY: wordpress-clean
wordpress-clean:
	rm -rf $(WP_DIR)
	echo "WordPress removed."

# PHP build targets (delegated to build/php/Makefile)
.PHONY: php
php:
	$(MAKE) -f $(ROOT)/build/php/Makefile download build

.PHONY: env
env:
	$(MAKE) -f $(ROOT)/build/php/Makefile env

.PHONY: php-clean
php-clean:
	$(MAKE) -f $(ROOT)/build/php/Makefile clean=1 clean
