/**
 * FrankenAsync Utility Functions
 */

#include <php.h>
#include <stdarg.h>

#include <Zend/zend_exceptions.h>

/**
 * Throws a PHP Exception with a formatted message
 */
void frankenasync_throw_exception(const char *format, ...) {
    va_list args;
    va_start(args, format);

    zend_string *message = zend_vstrpprintf(0, format, args);
    va_end(args);

    zend_throw_exception(zend_ce_exception, ZSTR_VAL(message), 0);
    zend_string_release(message);
}

/**
 * Throws a PHP Error with a formatted message
 */
void frankenasync_throw_error(const char *format, ...) {
    va_list args;
    va_start(args, format);

    zend_string *message = zend_vstrpprintf(0, format, args);
    va_end(args);

    zend_throw_exception_ex(zend_ce_error, E_ERROR, "%s", ZSTR_VAL(message));
    zend_string_release(message);
}

/**
 * Checks whether a HashTable is associative
 */
zend_bool frankenasync_is_associative(HashTable *ht) {
    zend_string *key;
    zend_ulong index;

    ZEND_HASH_FOREACH_KEY(ht, index, key) {
        (void)index;
        if (EXPECTED(key != NULL)) {
            return 1;
        }
    } ZEND_HASH_FOREACH_END();

    return 0;
}

/**
 * Check if a HashTable is a string-to-string map
 */
zend_bool frankenasync_is_string_map(HashTable *ht)
{
    if (UNEXPECTED(!ht)) {
        return 1;
    }

    /* First check if array is associative */
    if (UNEXPECTED(!frankenasync_is_associative(ht))) {
        return 0;
    }

    /* Then validate all values are strings */
    zend_string *key;
    zval *val;

    ZEND_HASH_FOREACH_STR_KEY_VAL(ht, key, val) {
        (void)key;
        if (UNEXPECTED(Z_TYPE_P(val) != IS_STRING)) {
            return 0;
        }
    } ZEND_HASH_FOREACH_END();

    return 1;
}
