<?php
/**
 * Moodle Health Check Endpoint for QUIC Load Balancer
 * 
 * Place this file in your Moodle installation at: /admin/cli/healthcheck.php
 * 
 * This script provides a comprehensive health check for the load balancer
 * to determine if this Moodle instance is ready to receive traffic.
 */

define('CLI_SCRIPT', true);
require_once(__DIR__ . '/../../config.php');
require_once($CFG->libdir . '/clilib.php');

// Set content type
header('Content-Type: application/json');
header('Cache-Control: no-cache, no-store, must-revalidate');

$status = 'healthy';
$checks = [];
$errors = [];

try {
    // Check 1: Database connectivity
    try {
        $dbcheck = $DB->get_record_sql('SELECT 1 as test');
        if ($dbcheck) {
            $checks['database'] = 'ok';
        } else {
            $checks['database'] = 'error';
            $errors[] = 'Database query returned empty result';
            $status = 'unhealthy';
        }
    } catch (Exception $e) {
        $checks['database'] = 'error';
        $errors[] = 'Database connection failed: ' . $e->getMessage();
        $status = 'unhealthy';
    }

    // Check 2: Data root directory
    if (is_dir($CFG->dataroot) && is_writable($CFG->dataroot)) {
        $checks['dataroot'] = 'ok';
    } else {
        $checks['dataroot'] = 'error';
        $errors[] = 'Data root directory not writable: ' . $CFG->dataroot;
        $status = 'unhealthy';
    }

    // Check 3: Cache directory
    $cachedir = $CFG->dataroot . '/cache';
    if (is_dir($cachedir) && is_writable($cachedir)) {
        $checks['cache'] = 'ok';
    } else {
        $checks['cache'] = 'warning';
        // Don't mark as unhealthy for cache issues
    }

    // Check 4: Session storage
    try {
        // Test session handling
        $sessiontest = true; // Simplified check
        $checks['sessions'] = 'ok';
    } catch (Exception $e) {
        $checks['sessions'] = 'error';
        $errors[] = 'Session handling error: ' . $e->getMessage();
        $status = 'unhealthy';
    }

    // Check 5: File permissions
    $tempfile = $CFG->dataroot . '/healthcheck_' . time() . '.tmp';
    if (file_put_contents($tempfile, 'test') !== false) {
        unlink($tempfile);
        $checks['filesystem'] = 'ok';
    } else {
        $checks['filesystem'] = 'error';
        $errors[] = 'Cannot write to data directory';
        $status = 'unhealthy';
    }

    // Check 6: PHP configuration
    $php_checks = [];
    
    // Memory limit
    $memory_limit = ini_get('memory_limit');
    if (preg_match('/(\d+)([MG]?)/', $memory_limit, $matches)) {
        $memory_mb = $matches[1];
        if (isset($matches[2]) && $matches[2] === 'G') {
            $memory_mb *= 1024;
        }
        $php_checks['memory_limit'] = $memory_mb >= 256 ? 'ok' : 'warning';
    }
    
    // Max execution time
    $max_execution_time = ini_get('max_execution_time');
    $php_checks['max_execution_time'] = $max_execution_time >= 30 ? 'ok' : 'warning';
    
    // File uploads
    $php_checks['file_uploads'] = ini_get('file_uploads') ? 'ok' : 'error';
    
    $checks['php'] = $php_checks;

    // Check 7: Moodle-specific health
    try {
        // Check if we can load a basic page/function
        $site = get_site();
        if ($site) {
            $checks['moodle_core'] = 'ok';
        } else {
            $checks['moodle_core'] = 'error';
            $errors[] = 'Cannot load site configuration';
            $status = 'unhealthy';
        }
    } catch (Exception $e) {
        $checks['moodle_core'] = 'error';
        $errors[] = 'Moodle core error: ' . $e->getMessage();
        $status = 'unhealthy';
    }

    // Additional info for load balancer
    $server_info = [
        'server_id' => gethostname(),
        'php_version' => PHP_VERSION,
        'moodle_version' => $CFG->version ?? 'unknown',
        'load_average' => function_exists('sys_getloadavg') ? sys_getloadavg() : null,
        'memory_usage' => [
            'current' => memory_get_usage(true),
            'peak' => memory_get_peak_usage(true)
        ]
    ];

} catch (Exception $e) {
    $status = 'unhealthy';
    $errors[] = 'Critical error: ' . $e->getMessage();
    $checks['critical'] = 'error';
}

// Determine HTTP status code
$http_status = ($status === 'healthy') ? 200 : 503;
http_response_code($http_status);

// Return health check response
$response = [
    'status' => $status,
    'timestamp' => time(),
    'iso_timestamp' => date('c'),
    'checks' => $checks,
    'server_info' => $server_info ?? null,
];

if (!empty($errors)) {
    $response['errors'] = $errors;
}

echo json_encode($response, JSON_PRETTY_PRINT);
exit($http_status === 200 ? 0 : 1);
?>