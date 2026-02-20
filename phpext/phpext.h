#ifndef FRANKENASYNC_H
#define FRANKENASYNC_H

#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>

#include <php.h>
#include <Zend/zend_types.h>

#define FRANKENASYNC_JSON_DEPTH 512

/* ============================================================================
 * SCRIPT CLASS
 * ============================================================================ */

/* Script object structure */
typedef struct _script_object {
    zend_string *name;
    HashTable *ini;
    zend_object std;
} script_object;

/* Script initialization */
int frankenasync_script_minit(void);

/* Script PHP methods */
PHP_METHOD(Script, __construct);
PHP_METHOD(Script, getName);
PHP_METHOD(Script, execute);
PHP_METHOD(Script, async);
PHP_METHOD(Script, defer);

/* Script argument info */
ZEND_BEGIN_ARG_INFO_EX(arginfo_frankenasync_script_construct, 0, 0, 1)
    ZEND_ARG_TYPE_INFO(0, name, IS_STRING, 0)
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, ini, IS_ARRAY, 1, "[]")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_frankenasync_script_get_name, 0, 0, IS_STRING, 0)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_frankenasync_script_execute, 0, 0, IS_ARRAY, 0)
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, app, IS_ARRAY, 1, "[]")
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, server, IS_ARRAY, 1, "[]")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_OBJ_INFO_EX(arginfo_frankenasync_script_async, 0, 0, Frankenphp\\Async\\Task, 0)
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, app, IS_ARRAY, 1, "[]")
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, server, IS_ARRAY, 1, "[]")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_OBJ_INFO_EX(arginfo_frankenasync_script_defer, 0, 0, Frankenphp\\Async\\Task, 0)
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, app, IS_ARRAY, 1, "[]")
    ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, server, IS_ARRAY, 1, "[]")
ZEND_END_ARG_INFO()

/* ============================================================================
 * ASYNC TASK CLASS
 * ============================================================================ */

/* AsyncTask initialization */
int frankenasync_asynctask_minit(void);

/* Retrieve the asynctask_object pointer from a zval */
#define Z_FRANKENASYNC_ASYNCTASK_OBJ_P(zv) frankenasync_asynctask_from_obj(Z_OBJ_P(zv))

/* Task class methods */
PHP_METHOD(Async_Task, __construct);
PHP_METHOD(Async_Task, getId);
PHP_METHOD(Async_Task, await);
PHP_METHOD(Async_Task, awaitAll);
PHP_METHOD(Async_Task, awaitAny);
PHP_METHOD(Async_Task, cancel);
PHP_METHOD(Async_Task, getStatus);
PHP_METHOD(Async_Task, getDuration);
PHP_METHOD(Async_Task, getError);

/* Helper to create Task object from C */
void frankenasync_create_asynctask_object(zval *return_value, const char *task_id);

/* Task class argument info */
ZEND_BEGIN_ARG_INFO_EX(arginfo_asynctask___construct, 0, 0, 1)
    ZEND_ARG_TYPE_INFO(0, taskId, IS_STRING, 0)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_getId, 0, 0, IS_STRING, 0)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_MASK_EX(arginfo_asynctask_await, 0, 0, MAY_BE_ARRAY | MAY_BE_STRING | MAY_BE_LONG | MAY_BE_DOUBLE | MAY_BE_NULL)
    ZEND_ARG_TYPE_MASK(0, timeout, MAY_BE_LONG | MAY_BE_STRING, "0")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_awaitAll, 0, 1, IS_ARRAY, 0)
    ZEND_ARG_TYPE_INFO(0, tasks, IS_ARRAY, 0)
    ZEND_ARG_TYPE_MASK(0, timeout, MAY_BE_LONG | MAY_BE_STRING, "0")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_awaitAny, 0, 1, IS_ARRAY, 0)
    ZEND_ARG_TYPE_INFO(0, tasks, IS_ARRAY, 0)
    ZEND_ARG_TYPE_MASK(0, timeout, MAY_BE_LONG | MAY_BE_STRING, "0")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_cancel, 0, 0, _IS_BOOL, 0)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_OBJ_INFO_EX(arginfo_asynctask_getStatus, 0, 0, Frankenphp\\Async\\Task\\Status, 0)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_getDuration, 0, 0, IS_DOUBLE, 1)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_getError, 0, 0, IS_STRING, 1)
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_asynctask_status___toString, 0, 0, IS_STRING, 0)
ZEND_END_ARG_INFO()

/* ============================================================================
 * MODULE LIFECYCLE
 * ============================================================================ */

void frankenasync_register();
int frankenasync_minit(int type, int module_number);
int frankenasync_mshutdown(int type, int module_number);
int frankenasync_rinit(int type, int module_number);
int frankenasync_rshutdown(int type, int module_number);

#endif // FRANKENASYNC_H
