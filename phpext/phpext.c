/**
 * FrankenAsync PHP Extension
 *
 * Registers Frankenphp\Script and Frankenphp\Async\Task classes.
 * Minimal extraction from Galvani's phpmodule.
 */

#include <php.h>
#include <php_ini.h>

#include <ext/standard/info.h>
#include <ext/json/php_json.h>
#include <ext/spl/spl_exceptions.h>

#include <Zend/zend_constants.h>
#include <Zend/zend_exceptions.h>
#include <Zend/zend_types.h>
#include <Zend/zend_hash.h>
#include <Zend/zend_smart_str.h>
#include <Zend/zend_enum.h>
#include <Zend/zend_interfaces.h>

#include "phpext.h"
#include "util.h"
#include "phpext_cgo.h"

#include "frankenphp.h"

/* ============================================================================
 * STATIC VARIABLES
 * ============================================================================ */

/* Script class */
static zend_class_entry *script_ce = NULL;
static zend_object_handlers script_object_handlers;

/* AsyncTask class */
static zend_class_entry *asynctask_ce;
static zend_class_entry *asynctask_status_ce;
static zend_object_handlers asynctask_object_handlers;

/* AsyncTask object structure */
typedef struct _frankenasync_asynctask_object {
    zend_string *task_id;
    zend_object std;
} frankenasync_asynctask_object;

/* Exception classes */
static zend_class_entry *asynctask_exception_ce;
static zend_class_entry *asynctask_timeout_ce;
static zend_class_entry *asynctask_failed_ce;
static zend_class_entry *asynctask_notfound_ce;
static zend_class_entry *asynctask_canceled_ce;
static zend_class_entry *asynctask_panic_ce;

/* ============================================================================
 * FORWARD DECLARATIONS
 * ============================================================================ */

/* Script */
static zend_object *script_create_object(zend_class_entry *ce);
static void script_free_object(zend_object *object);
static inline script_object *script_from_obj(zend_object *obj);
static int build_script_payload(smart_str *json_payload, const char *script_name, HashTable *ini, HashTable *app, HashTable *server);
static const zend_function_entry script_methods[];

/* AsyncTask */
static zend_object *asynctask_create_object(zend_class_entry *ce);
static void asynctask_free_object(zend_object *object);
static inline frankenasync_asynctask_object *frankenasync_asynctask_from_obj(zend_object *obj);
static inline void asynctask_throw_exception(const char *error_msg);
static const zend_function_entry asynctask_methods[];
static const zend_function_entry asynctask_status_methods[];

/* ============================================================================
 * MODULE LIFECYCLE
 * ============================================================================ */

static zend_module_entry frankenasync_module_entry = {
    STANDARD_MODULE_HEADER,
    "frankenasync",
    NULL, /* no global functions */
    frankenasync_minit,
    frankenasync_mshutdown,
    frankenasync_rinit,
    frankenasync_rshutdown,
    NULL, /* minfo */
    "0.1.0",
    STANDARD_MODULE_PROPERTIES
};

/* Use upstream FrankenPHP's register_extensions() API */
void frankenasync_register() {
    static zend_module_entry *modules[] = { &frankenasync_module_entry };
    register_extensions(modules, 1);
}

int frankenasync_minit(int type, int module_number) {
    /* Register Script class */
    if (frankenasync_script_minit() != SUCCESS) {
        php_error(E_WARNING, "Failed to register Frankenphp\\Script class.");
        return FAILURE;
    }

    /* Register AsyncTask class */
    if (frankenasync_asynctask_minit() != SUCCESS) {
        php_error(E_WARNING, "Failed to register Frankenphp\\Async\\Task class.");
        return FAILURE;
    }

    return SUCCESS;
}

int frankenasync_mshutdown(int type, int module_number) {
    return SUCCESS;
}

int frankenasync_rinit(int type, int module_number) {
    return SUCCESS;
}

int frankenasync_rshutdown(int type, int module_number) {
    return SUCCESS;
}

/* ============================================================================
 * SCRIPT CLASS IMPLEMENTATION
 * ============================================================================ */

int frankenasync_script_minit(void)
{
    zend_class_entry ce;

    INIT_NS_CLASS_ENTRY(ce, "Frankenphp", "Script", script_methods);

    script_ce = zend_register_internal_class(&ce);
    if (!script_ce) {
        return FAILURE;
    }

    script_ce->ce_flags |= ZEND_ACC_FINAL;
    script_ce->create_object = script_create_object;

    memcpy(&script_object_handlers, zend_get_std_object_handlers(), sizeof(zend_object_handlers));
    script_object_handlers.offset = XtOffsetOf(script_object, std);
    script_object_handlers.free_obj = script_free_object;

    return SUCCESS;
}

static zend_object *script_create_object(zend_class_entry *ce)
{
    script_object *intern = ecalloc(1, sizeof(script_object) + zend_object_properties_size(ce));

    zend_object_std_init(&intern->std, ce);
    object_properties_init(&intern->std, ce);

    intern->name = NULL;
    intern->ini = NULL;
    intern->std.handlers = &script_object_handlers;

    return &intern->std;
}

static void script_free_object(zend_object *object)
{
    script_object *intern = script_from_obj(object);

    if (intern->name) {
        zend_string_release(intern->name);
    }

    if (intern->ini) {
        zend_array_release(intern->ini);
    }

    zend_object_std_dtor(&intern->std);
}

PHP_METHOD(Script, __construct)
{
    zend_string *script_name;
    HashTable *ini = NULL;

    ZEND_PARSE_PARAMETERS_START(1, 2)
        Z_PARAM_STR(script_name)
        Z_PARAM_OPTIONAL
        Z_PARAM_ARRAY_HT_OR_NULL(ini)
    ZEND_PARSE_PARAMETERS_END();

    /* Validate INI array: must be associative with string values */
    if (ini && !frankenasync_is_string_map(ini)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'ini' parameter must be an associative array with string keys and string values");
        return;
    }

    script_object *intern = script_from_obj(Z_OBJ_P(ZEND_THIS));

    /* Resolve script name using include path if relative */
    zend_string *resolved_name = NULL;
    if (ZSTR_LEN(script_name) > 0 && ZSTR_VAL(script_name)[0] != '/') {
        resolved_name = php_resolve_path(ZSTR_VAL(script_name),
                                         ZSTR_LEN(script_name),
                                         PG(include_path));
    }

    if (resolved_name) {
        intern->name = resolved_name;
    } else {
        intern->name = zend_string_copy(script_name);
    }

    /* Store INI config array (copy if provided) */
    if (ini && zend_hash_num_elements(ini) > 0) {
        ALLOC_HASHTABLE(intern->ini);
        zend_hash_init(intern->ini, zend_hash_num_elements(ini), NULL, ZVAL_PTR_DTOR, 0);
        zend_hash_copy(intern->ini, ini, (copy_ctor_func_t) zval_add_ref);
    }
}

PHP_METHOD(Script, getName)
{
    ZEND_PARSE_PARAMETERS_NONE();

    script_object *intern = script_from_obj(Z_OBJ_P(ZEND_THIS));

    if (intern->name) {
        RETURN_STR_COPY(intern->name);
    }

    RETURN_NULL();
}

PHP_METHOD(Script, execute)
{
    HashTable *app = NULL;
    HashTable *server = NULL;
    smart_str json_payload = {0};

    ZEND_PARSE_PARAMETERS_START(0, 2)
        Z_PARAM_OPTIONAL
        Z_PARAM_ARRAY_HT_OR_NULL(app)
        Z_PARAM_ARRAY_HT_OR_NULL(server)
    ZEND_PARSE_PARAMETERS_END();

    script_object *intern = script_from_obj(Z_OBJ_P(ZEND_THIS));

    if (UNEXPECTED(!intern->name)) {
        frankenasync_throw_exception("Script object not properly initialized");
        RETURN_THROWS();
    }

    if (app && !frankenasync_is_associative(app)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'app' parameter must be an associative array with string keys");
        return;
    }

    if (server && !frankenasync_is_string_map(server)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'server' parameter must be an associative array with string keys and string values");
        return;
    }

    if (UNEXPECTED(build_script_payload(&json_payload, ZSTR_VAL(intern->name), intern->ini, app, server) == FAILURE)) {
        smart_str_free(&json_payload);
        frankenasync_throw_exception("Failed to encode payload");
        RETURN_THROWS();
    }

    struct go_execute_script_return result = go_execute_script(
        frankenphp_thread_index(),
        ZSTR_VAL(json_payload.s)
    );

    smart_str_free(&json_payload);

    if (UNEXPECTED(!result.r1)) {
        if (result.r0) {
            frankenasync_throw_exception("%s", result.r0);
            free(result.r0);
        } else {
            frankenasync_throw_error("Unknown internal error in runtime");
        }
        RETURN_THROWS();
    }

    if (UNEXPECTED(result.r0 == NULL)) {
        frankenasync_throw_exception("Received empty response for script '%s'", ZSTR_VAL(intern->name));
        RETURN_THROWS();
    }

    zval decoded_result;
    ZVAL_UNDEF(&decoded_result);

    if (UNEXPECTED(php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH) != SUCCESS)) {
        frankenasync_throw_error("Failed to decode data");
        free(result.r0);
        RETURN_THROWS();
    }

    free(result.r0);

    /* Remove internal fields from result */
    if (Z_TYPE(decoded_result) == IS_ARRAY) {
        zend_hash_str_del(Z_ARRVAL(decoded_result), "env", sizeof("env") - 1);
        zend_hash_str_del(Z_ARRVAL(decoded_result), "ini", sizeof("ini") - 1);
    }

    RETURN_ZVAL(&decoded_result, 1, 1);
}

PHP_METHOD(Script, async)
{
    HashTable *app = NULL;
    HashTable *server = NULL;
    smart_str json_payload = {0};

    ZEND_PARSE_PARAMETERS_START(0, 2)
        Z_PARAM_OPTIONAL
        Z_PARAM_ARRAY_HT_OR_NULL(app)
        Z_PARAM_ARRAY_HT_OR_NULL(server)
    ZEND_PARSE_PARAMETERS_END();

    script_object *intern = script_from_obj(Z_OBJ_P(ZEND_THIS));

    if (UNEXPECTED(!intern->name)) {
        frankenasync_throw_exception("Script object not properly initialized");
        RETURN_THROWS();
    }

    if (app && !frankenasync_is_associative(app)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'app' parameter must be an associative array with string keys");
        return;
    }

    if (server && !frankenasync_is_string_map(server)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'server' parameter must be an associative array with string keys and string values");
        return;
    }

    if (UNEXPECTED(build_script_payload(&json_payload, ZSTR_VAL(intern->name), intern->ini, app, server) == FAILURE)) {
        smart_str_free(&json_payload);
        frankenasync_throw_exception("Failed to encode payload");
        RETURN_THROWS();
    }

    struct go_execute_script_async_return result = go_execute_script_async(
        frankenphp_thread_index(),
        ZSTR_VAL(json_payload.s)
    );

    smart_str_free(&json_payload);

    if (UNEXPECTED(!result.r1)) {
        if (result.r0) {
            frankenasync_throw_exception("%s", result.r0);
            free(result.r0);
        } else {
            frankenasync_throw_error("Unknown internal error in runtime");
        }
        RETURN_THROWS();
    }

    if (UNEXPECTED(!result.r0)) {
        frankenasync_throw_exception("Failed to start asynchronous script execution for '%s'", ZSTR_VAL(intern->name));
        RETURN_THROWS();
    }

    frankenasync_create_asynctask_object(return_value, result.r0);
    free(result.r0);
}

PHP_METHOD(Script, defer)
{
    HashTable *app = NULL;
    HashTable *server = NULL;
    smart_str json_payload = {0};

    ZEND_PARSE_PARAMETERS_START(0, 2)
        Z_PARAM_OPTIONAL
        Z_PARAM_ARRAY_HT_OR_NULL(app)
        Z_PARAM_ARRAY_HT_OR_NULL(server)
    ZEND_PARSE_PARAMETERS_END();

    script_object *intern = script_from_obj(Z_OBJ_P(ZEND_THIS));

    if (UNEXPECTED(!intern->name)) {
        frankenasync_throw_exception("Script object not properly initialized");
        RETURN_THROWS();
    }

    if (app && !frankenasync_is_associative(app)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'app' parameter must be an associative array with string keys");
        return;
    }

    if (server && !frankenasync_is_string_map(server)) {
        zend_throw_exception_ex(spl_ce_InvalidArgumentException, 0,
            "The 'server' parameter must be an associative array with string keys and string values");
        return;
    }

    if (UNEXPECTED(build_script_payload(&json_payload, ZSTR_VAL(intern->name), intern->ini, app, server) == FAILURE)) {
        smart_str_free(&json_payload);
        frankenasync_throw_exception("Failed to encode payload");
        RETURN_THROWS();
    }

    struct go_execute_script_defer_return result = go_execute_script_defer(
        frankenphp_thread_index(),
        ZSTR_VAL(json_payload.s)
    );

    smart_str_free(&json_payload);

    if (UNEXPECTED(!result.r1)) {
        if (result.r0) {
            frankenasync_throw_exception("%s", result.r0);
            free(result.r0);
        } else {
            frankenasync_throw_error("Unknown internal error in runtime");
        }
        RETURN_THROWS();
    }

    if (UNEXPECTED(!result.r0)) {
        frankenasync_throw_exception("Failed to defer script execution for '%s'", ZSTR_VAL(intern->name));
        RETURN_THROWS();
    }

    frankenasync_create_asynctask_object(return_value, result.r0);
    free(result.r0);
}

PHP_METHOD(Script, __invoke)
{
    PHP_MN(Script_execute)(INTERNAL_FUNCTION_PARAM_PASSTHRU);
}

static const zend_function_entry script_methods[] = {
    PHP_ME(Script, __construct, arginfo_frankenasync_script_construct, ZEND_ACC_PUBLIC | ZEND_ACC_CTOR)
    PHP_ME(Script, getName, arginfo_frankenasync_script_get_name, ZEND_ACC_PUBLIC)
    PHP_ME(Script, execute, arginfo_frankenasync_script_execute, ZEND_ACC_PUBLIC)
    PHP_ME(Script, async, arginfo_frankenasync_script_async, ZEND_ACC_PUBLIC)
    PHP_ME(Script, defer, arginfo_frankenasync_script_defer, ZEND_ACC_PUBLIC)
    PHP_ME(Script, __invoke, arginfo_frankenasync_script_execute, ZEND_ACC_PUBLIC)
    PHP_FE_END
};

static inline script_object *script_from_obj(zend_object *obj) {
    return (script_object *)((char *)(obj) - XtOffsetOf(script_object, std));
}

static int build_script_payload(smart_str *json_payload, const char *script_name, HashTable *ini, HashTable *app, HashTable *server)
{
    zval payload_array;
    array_init(&payload_array);

    add_assoc_string(&payload_array, "name", script_name);

    if (ini && zend_hash_num_elements(ini) > 0) {
        zval ini_zval;
        ZVAL_ARR(&ini_zval, ini);
        Z_ADDREF(ini_zval);
        add_assoc_zval(&payload_array, "ini", &ini_zval);
    }

    zval env_array;
    array_init(&env_array);

    if (app && zend_hash_num_elements(app) > 0) {
        zval app_zval;
        ZVAL_ARR(&app_zval, app);
        Z_ADDREF(app_zval);
        add_assoc_zval(&env_array, "app", &app_zval);
    }

    if (server && zend_hash_num_elements(server) > 0) {
        zval server_zval;
        ZVAL_ARR(&server_zval, server);
        Z_ADDREF(server_zval);
        add_assoc_zval(&env_array, "cgi", &server_zval);
    }

    if (zend_hash_num_elements(Z_ARRVAL(env_array)) > 0) {
        add_assoc_zval(&payload_array, "env", &env_array);
    } else {
        zval_ptr_dtor(&env_array);
    }

    if (php_json_encode(json_payload, &payload_array, 0) != SUCCESS) {
        zval_ptr_dtor(&payload_array);
        return FAILURE;
    }

    smart_str_0(json_payload);
    zval_ptr_dtor(&payload_array);

    return SUCCESS;
}

/* ============================================================================
 * ASYNC TASK CLASS IMPLEMENTATION
 * ============================================================================ */

int frankenasync_asynctask_minit(void)
{
    /* Register Status enum first */
    asynctask_status_ce = zend_register_internal_enum(
        "Frankenphp\\Async\\Task\\Status", IS_STRING, NULL
    );

    if (UNEXPECTED(!asynctask_status_ce)) {
        return FAILURE;
    }

    /* Register the toString() method on the enum */
    zend_register_functions(
        asynctask_status_ce,
        asynctask_status_methods,
        &asynctask_status_ce->function_table,
        MODULE_PERSISTENT
    );

    /* Register enum cases with persistent strings */
    zval case_value;

    ZVAL_STR(&case_value, zend_string_init("deferred",   sizeof("deferred")-1,   1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Deferred", &case_value);
    zval_ptr_dtor(&case_value);

    ZVAL_STR(&case_value, zend_string_init("pending",   sizeof("pending")-1,   1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Pending", &case_value);
    zval_ptr_dtor(&case_value);

    ZVAL_STR(&case_value, zend_string_init("running",   sizeof("running")-1,   1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Running", &case_value);
    zval_ptr_dtor(&case_value);

    ZVAL_STR(&case_value, zend_string_init("completed", sizeof("completed")-1, 1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Completed", &case_value);
    zval_ptr_dtor(&case_value);

    ZVAL_STR(&case_value, zend_string_init("failed",    sizeof("failed")-1,    1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Failed", &case_value);
    zval_ptr_dtor(&case_value);

    ZVAL_STR(&case_value, zend_string_init("canceled",  sizeof("canceled")-1,  1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Canceled", &case_value);
    zval_ptr_dtor(&case_value);

    ZVAL_STR(&case_value, zend_string_init("unknown",   sizeof("unknown")-1,   1));
    zend_enum_add_case_cstr(asynctask_status_ce, "Unknown", &case_value);
    zval_ptr_dtor(&case_value);

    zend_class_entry ce;

    /* Register exception hierarchy */
    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async\\Task", "Exception", NULL);
    asynctask_exception_ce = zend_register_internal_class_ex(&ce, zend_ce_exception);

    zend_declare_property_null(asynctask_exception_ce, "taskId", sizeof("taskId")-1, ZEND_ACC_PROTECTED);

    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async\\Task", "TimeoutException", NULL);
    asynctask_timeout_ce = zend_register_internal_class_ex(&ce, asynctask_exception_ce);

    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async\\Task", "FailedException", NULL);
    asynctask_failed_ce = zend_register_internal_class_ex(&ce, asynctask_exception_ce);

    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async\\Task", "NotFoundException", NULL);
    asynctask_notfound_ce = zend_register_internal_class_ex(&ce, asynctask_exception_ce);

    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async\\Task", "CanceledException", NULL);
    asynctask_canceled_ce = zend_register_internal_class_ex(&ce, asynctask_exception_ce);

    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async\\Task", "TaskPanicException", NULL);
    asynctask_panic_ce = zend_register_internal_class_ex(&ce, asynctask_exception_ce);

    /* Register Task class */
    INIT_NS_CLASS_ENTRY(ce, "Frankenphp\\Async", "Task", asynctask_methods);
    asynctask_ce = zend_register_internal_class(&ce);

    if (UNEXPECTED(!asynctask_ce)) {
        return FAILURE;
    }

    asynctask_ce->create_object = asynctask_create_object;

    memcpy(&asynctask_object_handlers, zend_get_std_object_handlers(), sizeof(zend_object_handlers));
    asynctask_object_handlers.offset = XtOffsetOf(frankenasync_asynctask_object, std);
    asynctask_object_handlers.free_obj = asynctask_free_object;

    return SUCCESS;
}

static zend_object *asynctask_create_object(zend_class_entry *ce)
{
    frankenasync_asynctask_object *intern = ecalloc(1, sizeof(frankenasync_asynctask_object) + zend_object_properties_size(ce));

    zend_object_std_init(&intern->std, ce);
    object_properties_init(&intern->std, ce);

    intern->task_id = NULL;
    intern->std.handlers = &asynctask_object_handlers;

    return &intern->std;
}

static void asynctask_free_object(zend_object *object)
{
    frankenasync_asynctask_object *intern = frankenasync_asynctask_from_obj(object);

    if (EXPECTED(intern->task_id)) {
        zend_string_release(intern->task_id);
    }

    zend_object_std_dtor(&intern->std);
}

PHP_METHOD(Async_Task, __construct)
{
    zend_string *task_id;

    ZEND_PARSE_PARAMETERS_START(1, 1)
        Z_PARAM_STR(task_id)
    ZEND_PARSE_PARAMETERS_END();

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);
    intern->task_id = zend_string_copy(task_id);
}

PHP_METHOD(Async_Task, getId)
{
    ZEND_PARSE_PARAMETERS_NONE();

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);

    if (UNEXPECTED(!intern->task_id)) {
        frankenasync_throw_error("Task ID not set");
        RETURN_THROWS();
    }

    RETURN_STR_COPY(intern->task_id);
}

PHP_METHOD(Async_Task, await)
{
    zval *timeout_param = NULL;

    ZEND_PARSE_PARAMETERS_START(0, 1)
        Z_PARAM_OPTIONAL
        Z_PARAM_ZVAL(timeout_param)
    ZEND_PARSE_PARAMETERS_END();

    PARSE_TIMEOUT_PARAM(timeout_param)

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);

    if (UNEXPECTED(!intern->task_id)) {
        frankenasync_throw_error("Task ID not set");
        RETURN_THROWS();
    }

    struct go_asynctask_await_return result = go_asynctask_await(
        frankenphp_thread_index(),
        ZSTR_VAL(intern->task_id),
        timeout_ms
    );

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    if (UNEXPECTED(result.r0 == NULL)) {
        RETURN_NULL();
    }

    zval decoded_result;
    ZVAL_UNDEF(&decoded_result);

    zend_try {
        php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH);

        if (EXPECTED(Z_TYPE(decoded_result) == IS_ARRAY)) {
            free(result.r0);
            RETURN_ZVAL(&decoded_result, 1, 1);
        }

        RETVAL_STRING(result.r0);
        free(result.r0);
        zval_ptr_dtor(&decoded_result);

    } zend_catch {
        free(result.r0);
        zend_bailout();
        RETURN_THROWS();
    } zend_end_try();
}

PHP_METHOD(Async_Task, awaitAll)
{
    zval *tasks_array;
    zval *timeout_param = NULL;

    ZEND_PARSE_PARAMETERS_START(1, 2)
        Z_PARAM_ARRAY(tasks_array)
        Z_PARAM_OPTIONAL
        Z_PARAM_ZVAL(timeout_param)
    ZEND_PARSE_PARAMETERS_END();

    PARSE_TIMEOUT_PARAM(timeout_param)

    HashTable *tasks_ht = Z_ARRVAL_P(tasks_array);
    uint32_t task_count = zend_hash_num_elements(tasks_ht);

    if (EXPECTED(task_count == 0)) {
        array_init(return_value);
        return;
    }

    zval task_ids_array;
    array_init(&task_ids_array);

    zval *task_obj;
    ZEND_HASH_FOREACH_VAL(tasks_ht, task_obj) {
        if (UNEXPECTED(Z_TYPE_P(task_obj) != IS_OBJECT ||
            !instanceof_function(Z_OBJCE_P(task_obj), asynctask_ce))) {
            zval_ptr_dtor(&task_ids_array);
            frankenasync_throw_error("All elements must be Task objects");
            RETURN_THROWS();
        }

        frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(task_obj);
        if (UNEXPECTED(!intern->task_id)) {
            zval_ptr_dtor(&task_ids_array);
            frankenasync_throw_error("Task has no task ID");
            RETURN_THROWS();
        }

        add_next_index_str(&task_ids_array, zend_string_copy(intern->task_id));
    } ZEND_HASH_FOREACH_END();

    smart_str json_task_ids = {0};
    php_json_encode(&json_task_ids, &task_ids_array, PHP_JSON_THROW_ON_ERROR);
    smart_str_0(&json_task_ids);

    zval_ptr_dtor(&task_ids_array);

    struct go_asynctask_await_all_return result = go_asynctask_await_all(
        frankenphp_thread_index(),
        ZSTR_VAL(json_task_ids.s),
        timeout_ms
    );

    smart_str_free(&json_task_ids);

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    if (UNEXPECTED(result.r0 == NULL)) {
        RETURN_NULL();
    }

    zval decoded_result;
    ZVAL_UNDEF(&decoded_result);

    zend_try {
        php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH);

        if (EXPECTED(Z_TYPE(decoded_result) == IS_ARRAY)) {
            free(result.r0);
            RETURN_ZVAL(&decoded_result, 1, 1);
        }

        RETVAL_STRING(result.r0);
        free(result.r0);
        zval_ptr_dtor(&decoded_result);

    } zend_catch {
        free(result.r0);
        zend_bailout();
        RETURN_THROWS();
    } zend_end_try();
}

PHP_METHOD(Async_Task, awaitAny)
{
    zval *tasks_array;
    zval *timeout_param = NULL;

    ZEND_PARSE_PARAMETERS_START(1, 2)
        Z_PARAM_ARRAY(tasks_array)
        Z_PARAM_OPTIONAL
        Z_PARAM_ZVAL(timeout_param)
    ZEND_PARSE_PARAMETERS_END();

    PARSE_TIMEOUT_PARAM(timeout_param)

    HashTable *tasks_ht = Z_ARRVAL_P(tasks_array);
    uint32_t task_count = zend_hash_num_elements(tasks_ht);

    if (EXPECTED(task_count == 0)) {
        RETURN_NULL();
    }

    zval task_ids_array;
    array_init(&task_ids_array);

    zval *task_obj;
    ZEND_HASH_FOREACH_VAL(tasks_ht, task_obj) {
        if (UNEXPECTED(Z_TYPE_P(task_obj) != IS_OBJECT ||
            !instanceof_function(Z_OBJCE_P(task_obj), asynctask_ce))) {
            zval_ptr_dtor(&task_ids_array);
            frankenasync_throw_error("All elements must be Task objects");
            RETURN_THROWS();
        }

        frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(task_obj);
        if (UNEXPECTED(!intern->task_id)) {
            zval_ptr_dtor(&task_ids_array);
            frankenasync_throw_error("Task has no task ID");
            RETURN_THROWS();
        }

        add_next_index_str(&task_ids_array, zend_string_copy(intern->task_id));
    } ZEND_HASH_FOREACH_END();

    smart_str json_task_ids = {0};
    php_json_encode(&json_task_ids, &task_ids_array, PHP_JSON_THROW_ON_ERROR);
    smart_str_0(&json_task_ids);

    zval_ptr_dtor(&task_ids_array);

    struct go_asynctask_await_any_return result = go_asynctask_await_any(
        frankenphp_thread_index(),
        ZSTR_VAL(json_task_ids.s),
        timeout_ms
    );

    smart_str_free(&json_task_ids);

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    if (UNEXPECTED(result.r0 == NULL)) {
        RETURN_NULL();
    }

    zval decoded_result;
    ZVAL_UNDEF(&decoded_result);

    zend_try {
        php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH);

        if (EXPECTED(Z_TYPE(decoded_result) == IS_ARRAY)) {
            free(result.r0);
            RETURN_ZVAL(&decoded_result, 1, 1);
        }

        RETVAL_STRING(result.r0);
        free(result.r0);
        zval_ptr_dtor(&decoded_result);

    } zend_catch {
        free(result.r0);
        zend_bailout();
        RETURN_THROWS();
    } zend_end_try();
}

PHP_METHOD(Async_Task, cancel)
{
    ZEND_PARSE_PARAMETERS_NONE();

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);

    if (UNEXPECTED(!intern->task_id)) {
        frankenasync_throw_error("Task ID not set");
        RETURN_THROWS();
    }

    struct go_asynctask_cancel_return result = go_asynctask_cancel(
        frankenphp_thread_index(),
        ZSTR_VAL(intern->task_id)
    );

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    if (EXPECTED(result.r0 != NULL)) {
        free(result.r0);
    }

    RETURN_BOOL(1);
}

PHP_METHOD(Async_Task, getStatus)
{
    ZEND_PARSE_PARAMETERS_NONE();

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);

    if (UNEXPECTED(!intern->task_id)) {
        frankenasync_throw_error("Task ID not set");
        RETURN_THROWS();
    }

    struct go_asynctask_info_return result = go_asynctask_info(
        frankenphp_thread_index(),
        ZSTR_VAL(intern->task_id)
    );

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    char status_buf[32];
    const char *status_str = "unknown";

    if (EXPECTED(result.r0 != NULL)) {
        zval decoded_result;
        ZVAL_UNDEF(&decoded_result);

        if (EXPECTED(php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH) == SUCCESS)) {
            if (EXPECTED(Z_TYPE(decoded_result) == IS_ARRAY)) {
                zval *status_val = zend_hash_str_find(Z_ARRVAL(decoded_result), "status", sizeof("status") - 1);
                if (EXPECTED(status_val && Z_TYPE_P(status_val) == IS_STRING)) {
                    size_t len = Z_STRLEN_P(status_val);
                    if (len < sizeof(status_buf)) {
                        memcpy(status_buf, Z_STRVAL_P(status_val), len + 1);
                        status_str = status_buf;
                    }
                }
            }
            zval_ptr_dtor(&decoded_result);
        }
        free(result.r0);
    }

    /* Call Status::from($status_str) to get enum object */
    zval arg, retval;
    ZVAL_STRING(&arg, status_str);
    ZVAL_UNDEF(&retval);

    zend_call_method(
        NULL,
        asynctask_status_ce,
        NULL,
        "from",
        sizeof("from") - 1,
        &retval,
        1,
        &arg,
        NULL
    );

    zval_ptr_dtor(&arg);

    if (EXPECTED(!Z_ISUNDEF(retval) && Z_TYPE(retval) == IS_OBJECT)) {
        RETURN_ZVAL(&retval, 0, 0);
    }

    if (EXPECTED(!Z_ISUNDEF(retval))) {
        zval_ptr_dtor(&retval);
    }

    RETURN_NULL();
}

PHP_METHOD(Async_Task, getDuration)
{
    ZEND_PARSE_PARAMETERS_NONE();

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);

    if (UNEXPECTED(!intern->task_id)) {
        frankenasync_throw_error("Task ID not set");
        RETURN_THROWS();
    }

    struct go_asynctask_info_return result = go_asynctask_info(
        frankenphp_thread_index(),
        ZSTR_VAL(intern->task_id)
    );

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    if (UNEXPECTED(result.r0 == NULL)) {
        RETURN_NULL();
    }

    zval decoded_result;
    ZVAL_UNDEF(&decoded_result);

    if (UNEXPECTED(php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH) != SUCCESS)) {
        frankenasync_throw_error("Failed to decode task info");
        free(result.r0);
        RETURN_THROWS();
    }

    free(result.r0);

    if (EXPECTED(Z_TYPE(decoded_result) == IS_ARRAY)) {
        zval *duration_val = zend_hash_str_find(Z_ARRVAL(decoded_result), "duration", sizeof("duration") - 1);
        if (EXPECTED(duration_val)) {
            if (EXPECTED(Z_TYPE_P(duration_val) == IS_DOUBLE)) {
                double duration = Z_DVAL_P(duration_val);
                zval_ptr_dtor(&decoded_result);
                RETURN_DOUBLE(duration);
            } else if (EXPECTED(Z_TYPE_P(duration_val) == IS_LONG)) {
                double duration = (double)Z_LVAL_P(duration_val);
                zval_ptr_dtor(&decoded_result);
                RETURN_DOUBLE(duration);
            }
        }
    }

    zval_ptr_dtor(&decoded_result);
    RETURN_NULL();
}

PHP_METHOD(Async_Task, getError)
{
    ZEND_PARSE_PARAMETERS_NONE();

    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(ZEND_THIS);

    if (UNEXPECTED(!intern->task_id)) {
        frankenasync_throw_error("Task ID not set");
        RETURN_THROWS();
    }

    struct go_asynctask_info_return result = go_asynctask_info(
        frankenphp_thread_index(),
        ZSTR_VAL(intern->task_id)
    );

    if (UNEXPECTED(!result.r1)) {
        asynctask_throw_exception(result.r0);
        free(result.r0);
        RETURN_THROWS();
    }

    if (UNEXPECTED(result.r0 == NULL)) {
        RETURN_NULL();
    }

    zval decoded_result;
    ZVAL_UNDEF(&decoded_result);

    if (UNEXPECTED(php_json_decode_ex(&decoded_result, result.r0, strlen(result.r0), PHP_JSON_OBJECT_AS_ARRAY, FRANKENASYNC_JSON_DEPTH) != SUCCESS)) {
        frankenasync_throw_error("Failed to decode task info");
        free(result.r0);
        RETURN_THROWS();
    }

    free(result.r0);

    if (EXPECTED(Z_TYPE(decoded_result) == IS_ARRAY)) {
        zval *error_val = zend_hash_str_find(Z_ARRVAL(decoded_result), "error", sizeof("error") - 1);
        if (EXPECTED(error_val && Z_TYPE_P(error_val) == IS_STRING)) {
            zend_string *error_str = zend_string_copy(Z_STR_P(error_val));
            zval_ptr_dtor(&decoded_result);
            RETURN_STR(error_str);
        }
    }

    zval_ptr_dtor(&decoded_result);
    RETURN_NULL();
}

PHP_METHOD(Async_Task_Status, __toString)
{
    ZEND_PARSE_PARAMETERS_NONE();

    zval *this_ptr = ZEND_THIS;

    if (UNEXPECTED(Z_TYPE_P(this_ptr) != IS_OBJECT)) {
        RETURN_EMPTY_STRING();
    }

    zval *value = zend_read_property(
        Z_OBJCE_P(this_ptr),
        Z_OBJ_P(this_ptr),
        "value",
        sizeof("value") - 1,
        1,
        NULL
    );

    if (EXPECTED(value && Z_TYPE_P(value) == IS_STRING)) {
        RETURN_STR_COPY(Z_STR_P(value));
    }

    RETURN_EMPTY_STRING();
}

static const zend_function_entry asynctask_methods[] = {
    PHP_ME(Async_Task, __construct, arginfo_asynctask___construct, ZEND_ACC_PRIVATE)
    PHP_ME(Async_Task, getId, arginfo_asynctask_getId, ZEND_ACC_PUBLIC)
    PHP_ME(Async_Task, await, arginfo_asynctask_await, ZEND_ACC_PUBLIC)
    PHP_ME(Async_Task, awaitAll, arginfo_asynctask_awaitAll, ZEND_ACC_PUBLIC | ZEND_ACC_STATIC)
    PHP_ME(Async_Task, awaitAny, arginfo_asynctask_awaitAny, ZEND_ACC_PUBLIC | ZEND_ACC_STATIC)
    PHP_ME(Async_Task, cancel, arginfo_asynctask_cancel, ZEND_ACC_PUBLIC)
    PHP_ME(Async_Task, getStatus, arginfo_asynctask_getStatus, ZEND_ACC_PUBLIC)
    PHP_ME(Async_Task, getDuration, arginfo_asynctask_getDuration, ZEND_ACC_PUBLIC)
    PHP_ME(Async_Task, getError, arginfo_asynctask_getError, ZEND_ACC_PUBLIC)
    PHP_FE_END
};

static const zend_function_entry asynctask_status_methods[] = {
    PHP_ME(Async_Task_Status, __toString, arginfo_asynctask_status___toString, ZEND_ACC_PUBLIC)
    PHP_FE_END
};

/* ============================================================================
 * HELPER FUNCTIONS
 * ============================================================================ */

static inline frankenasync_asynctask_object *frankenasync_asynctask_from_obj(zend_object *obj) {
    return (frankenasync_asynctask_object *)((char *)(obj) - XtOffsetOf(frankenasync_asynctask_object, std));
}

void frankenasync_create_asynctask_object(zval *return_value, const char *task_id)
{
    object_init_ex(return_value, asynctask_ce);
    frankenasync_asynctask_object *intern = Z_FRANKENASYNC_ASYNCTASK_OBJ_P(return_value);
    intern->task_id = zend_string_init(task_id, strlen(task_id), 0);
}

static inline void asynctask_throw_exception(const char *error_msg) {
    zend_class_entry *exception_ce = asynctask_exception_ce;
    const char *task_id_str = NULL;

    /* Extract task ID if present */
    if (EXPECTED(strncmp(error_msg, "task ", 5) == 0 && strlen(error_msg) > 26 && error_msg[25] == ':')) {
        task_id_str = error_msg + 5;
    }

    /* Determine exception type */
    if (strstr(error_msg, "task timed out")) {
        exception_ce = asynctask_timeout_ce;
    } else if (strstr(error_msg, "task not found")) {
        exception_ce = asynctask_notfound_ce;
    } else if (strstr(error_msg, "task canceled")) {
        exception_ce = asynctask_canceled_ce;
    } else if (strstr(error_msg, "task panicked")) {
        exception_ce = asynctask_panic_ce;
    } else if (strstr(error_msg, "task failed")) {
        exception_ce = asynctask_failed_ce;
    }

    zend_throw_exception(exception_ce, error_msg, 0);

    /* Set taskId property if extracted */
    if (EXPECTED(EG(exception) && task_id_str != NULL)) {
        zval task_id_zval;
        ZVAL_STRINGL(&task_id_zval, task_id_str, 20);
        zend_update_property(
            exception_ce,
            EG(exception),
            "taskId",
            sizeof("taskId")-1,
            &task_id_zval
        );
        zval_ptr_dtor(&task_id_zval);
    }
}
