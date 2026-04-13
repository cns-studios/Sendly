(function() {
    'use strict';

     
    let fileMetadata = null;
    let encryptedBlob = null;
    let currentPassword = null;

     
    const loadingSection = document.getElementById('loading-section');
    const passwordSection = document.getElementById('password-section');
    const autoDecryptSection = document.getElementById('auto-decrypt-section');
    const manualDecryptSection = document.getElementById('manual-decrypt-section');
    const progressSection = document.getElementById('progress-section');
    const successSection = document.getElementById('success-section');
    const errorSection = document.getElementById('error-section');

    const fileNameEl = document.getElementById('file-name');
    const fileSizeEl = document.getElementById('file-size');
    const fileCreatedEl = document.getElementById('file-created');
    const fileExpiresEl = document.getElementById('file-expires');

    const passwordInput = document.getElementById('password-input');
    const downloadAutoBtn = document.getElementById('download-auto-btn');
    const downloadManualBtn = document.getElementById('download-manual-btn');
    const downloadAgainBtn = document.getElementById('download-again-btn');
    const retryBtn = document.getElementById('retry-btn');

    const progressFill = document.getElementById('progress-fill');
    const progressText = document.getElementById('progress-text');
    const errorTitle = document.getElementById('error-title');
    const errorMessage = document.getElementById('error-message');

    const reportBtn = document.getElementById('report-btn');
    const reportModal = document.getElementById('report-modal');
    const reportCancel = document.getElementById('report-cancel');
    const reportConfirm = document.getElementById('report-confirm');

    function getCookieValue(name) {
        const value = `; ${document.cookie}`;
        const parts = value.split(`; ${name}=`);
        if (parts.length === 2) {
            return parts.pop().split(';').shift();
        }
        return '';
    }

    async function init() {
        const fileID = window.CONFIG?.fileID;
        
        if (!fileID) {
            showError('Invalid URL', 'No file ID provided');
            return;
        }

        setupEventListeners();
        await loadFileMetadata(fileID);
    }

    function setupEventListeners() {
        downloadAutoBtn?.addEventListener('click', () => downloadAndDecrypt(currentPassword));
        downloadManualBtn?.addEventListener('click', () => {
            const password = passwordInput.value.trim().toLowerCase();
            downloadAndDecrypt(password);
        });
        downloadAgainBtn?.addEventListener('click', () => downloadAndDecrypt(currentPassword));
        retryBtn?.addEventListener('click', resetToPasswordEntry);

         
        passwordInput?.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') {
                downloadManualBtn.click();
            }
        });

         
        reportBtn?.addEventListener('click', () => {
            reportModal.classList.remove('hidden');
        });

        reportCancel?.addEventListener('click', () => {
            reportModal.classList.add('hidden');
        });

        reportConfirm?.addEventListener('click', submitReport);

         
        reportModal?.addEventListener('click', (e) => {
            if (e.target === reportModal) {
                reportModal.classList.add('hidden');
            }
        });
    }

    async function loadFileMetadata(fileID) {
        try {
            const response = await fetch(`/api/file/${fileID}`);
            
            if (!response.ok) {
                const error = await response.json();
                handleAPIError(error);
                return;
            }

            fileMetadata = await response.json();
            displayFileMetadata();

             
            const hashPassword = SecureCrypto.getPasswordFromHash();
            if (hashPassword) {
                const validation = SecureCrypto.validatePassword(hashPassword);
                if (validation.valid) {
                    currentPassword = hashPassword;
                    autoDecryptSection.classList.remove('hidden');
                    manualDecryptSection.classList.add('hidden');
                } else {
                    manualDecryptSection.classList.remove('hidden');
                }
            } else {
                manualDecryptSection.classList.remove('hidden');
            }

            loadingSection.classList.add('hidden');
            passwordSection.classList.remove('hidden');

        } catch (error) {
            console.error('Failed to load file metadata:', error);
            showError('Connection Error', 'Failed to connect to server. Please try again.');
        }
    }

    function displayFileMetadata() {
        fileNameEl.textContent = fileMetadata.original_name;
        fileSizeEl.textContent = SecureCrypto.formatFileSize(fileMetadata.size_bytes);
        fileCreatedEl.textContent = SecureCrypto.formatDate(fileMetadata.created_at);
        fileExpiresEl.textContent = SecureCrypto.getTimeRemaining(fileMetadata.expires_at);
    }

    async function downloadAndDecrypt(password) {
         
        const validation = SecureCrypto.validatePassword(password);
        if (!validation.valid) {
            showError('Invalid Passcode', validation.error);
            retryBtn.classList.remove('hidden');
            return;
        }

        currentPassword = password;

        
        loadingSection.classList.add('hidden');
        passwordSection.classList.remove('hidden');
        autoDecryptSection.classList.add('hidden');
        manualDecryptSection.classList.add('hidden');
        progressSection.classList.remove('hidden');
        successSection.classList.add('hidden');
        errorSection.classList.add('hidden');

        
        await new Promise((resolve) => requestAnimationFrame(resolve));

        try {
             
            updateProgress(0, 'Downloading file...');
            encryptedBlob = await downloadEncryptedFile();

             
            updateProgress(50, 'Decrypting file...');
            const decryptedData = await SecureCrypto.decryptBlob(
                encryptedBlob,
                password,
                (progress, status) => {
                    updateProgress(50 + (progress * 0.4), status);
                }
            );

             
            updateProgress(95, 'Preparing download...');
            triggerDownload(decryptedData, fileMetadata.original_name);

            updateProgress(100, 'Complete!');

             
            passwordSection.classList.add('hidden');
            progressSection.classList.add('hidden');
            successSection.classList.remove('hidden');

        } catch (error) {
            console.error('Download/decrypt failed:', error);
            progressSection.classList.add('hidden');
            
            if (error.message.includes('Decryption failed')) {
                showError('Decryption Failed', 'Invalid passcode. Please check and try again.');
                retryBtn.classList.remove('hidden');
            } else {
                showError('Download Failed', error.message);
                retryBtn.classList.remove('hidden');
            }
        }
    }

    async function downloadEncryptedFile() {
        const response = await fetch(`/api/file/${fileMetadata.id}/download`);
        
        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.error || 'Failed to download file');
        }

        const contentLength = response.headers.get('Content-Length');
        const total = parseInt(contentLength, 10);
        const reader = response.body.getReader();
        const chunks = [];
        let received = 0;

        while (true) {
            const { done, value } = await reader.read();
            
            if (done) break;

            chunks.push(value);
            received += value.length;

            if (total) {
                const progress = (received / total) * 50;
                updateProgress(progress, `Downloading... ${SecureCrypto.formatFileSize(received)} / ${SecureCrypto.formatFileSize(total)}`);
            }
        }

        return new Blob(chunks);
    }

    function triggerDownload(data, filename) {
        const blob = new Blob([data], { type: 'application/octet-stream' });
        const url = URL.createObjectURL(blob);
        
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        a.style.display = 'none';
        
        document.body.appendChild(a);
        a.click();
        
         
        setTimeout(() => {
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        }, 100);
    }

    async function submitReport() {
        reportConfirm.disabled = true;
        reportConfirm.textContent = 'Reporting...';

        try {
            const response = await fetch(`/api/file/${fileMetadata.id}/report`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                }
            });

            const result = await response.json();

            if (response.ok) {
                reportModal.classList.add('hidden');
                showToast('Report submitted. Thank you for helping keep our platform safe.');
            } else {
                showToast(result.error || 'Failed to submit report');
            }
        } catch (error) {
            console.error('Report failed:', error);
            showToast('Failed to submit report. Please try again.');
        } finally {
            reportConfirm.disabled = false;
            reportConfirm.textContent = 'Report';
        }
    }

    function handleAPIError(error) {
        loadingSection.classList.add('hidden');
        
        switch (error.code) {
            case 'FILE_NOT_FOUND':
                showError('File Not Found', 'This file does not exist or has been removed.');
                break;
            case 'FILE_EXPIRED':
                showError('File Expired', 'This file has expired and is no longer available.');
                break;
            case 'FILE_DELETED':
                showError('File Removed', 'This file has been removed due to policy violations.');
                break;
            default:
                showError('Error', error.error || 'An unexpected error occurred.');
        }
    }

    function showError(title, message) {
        errorTitle.textContent = title;
        errorMessage.textContent = message;
        
        loadingSection.classList.add('hidden');
        passwordSection.classList.add('hidden');
        progressSection.classList.add('hidden');
        successSection.classList.add('hidden');
        errorSection.classList.remove('hidden');
    }

    function resetToPasswordEntry() {
        errorSection.classList.add('hidden');
        passwordSection.classList.remove('hidden');
        
         
        autoDecryptSection.classList.add('hidden');
        manualDecryptSection.classList.remove('hidden');
        
        passwordInput.value = currentPassword || '';
        passwordInput.focus();
    }

    function updateProgress(percent, text) {
        if (text) {
            progressText.textContent = text;
        }
    }

    function showToast(message) {
        const existingToast = document.querySelector('.toast');
        if (existingToast) {
            existingToast.remove();
        }

        const toast = document.createElement('div');
        toast.className = 'toast';
        toast.textContent = message;
        toast.style.cssText = `
            position: fixed;
            bottom: 20px;
            left: 50%;
            transform: translateX(-50%);
            background-color: #333;
            color: white;
            padding: 12px 24px;
            border-radius: 8px;
            z-index: 1001;
            max-width: 90%;
            text-align: center;
        `;

        document.body.appendChild(toast);

        setTimeout(() => {
            toast.remove();
        }, 4000);
    }

     
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();