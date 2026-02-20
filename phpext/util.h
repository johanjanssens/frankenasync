#ifndef FRANKENASYNC_UTIL_H
#define FRANKENASYNC_UTIL_H

#include <php.h>

/**
 * Parse timeout parameter which can be int (milliseconds) or string (duration)
 * Usage: PARSE_TIMEOUT_PARAM(timeout_param)
 * Creates a 'timeout_ms' variable in the current scope
 * Returns early with RETURN_THROWS() on parse error
 */
#define PARSE_TIMEOUT_PARAM(timeout_param) \
    zend_long timeout_ms = 0; \
    if (timeout_param) { \
        if (Z_TYPE_P(timeout_param) == IS_STRING) { \
            long long parsed_ms = go_parse_duration_ms(Z_STRVAL_P(timeout_param)); \
            if (parsed_ms < 0) { \
                frankenasync_throw_error("Invalid duration format: %s", Z_STRVAL_P(timeout_param)); \
                RETURN_THROWS(); \
            } \
            timeout_ms = (zend_long)parsed_ms; \
        } else if (Z_TYPE_P(timeout_param) == IS_LONG) { \
            timeout_ms = Z_LVAL_P(timeout_param); \
        } else { \
            frankenasync_throw_error("Timeout must be an integer (milliseconds) or duration string"); \
            RETURN_THROWS(); \
        } \
    }

/**
 * Throws a recoverable exception with formatted message
 */
void frankenasync_throw_exception(const char *format, ...);

/**
 * Throws a fatal error with formatted message
 */
void frankenasync_throw_error(const char *format, ...);

/**
 * Check if a HashTable is associative (has at least one string key)
 */
zend_bool frankenasync_is_associative(HashTable *ht);

/**
 * Check if a HashTable is a string-to-string map
 */
zend_bool frankenasync_is_string_map(HashTable *ht);

#endif /* FRANKENASYNC_UTIL_H */
