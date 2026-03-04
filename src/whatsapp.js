const { 
    default: makeWASocket, 
    useMultiFileAuthState, 
    DisconnectReason,
    fetchLatestBaileysVersion,
    Browsers
} = require('@whiskeysockets/baileys');
const pino = require('pino');
const QRCode = require('qrcode');
const { handleMessage, handleHistorySync } = require('./messageHandler');

// Move backoff tracking outside the function so it isn't reset on every call
let backoffMs = 1_000;
const nextBackoff = () => (backoffMs = Math.min(backoffMs * 2, 30_000));
const resetBackoff = () => (backoffMs = 1_000);

async function connectToWhatsApp() {
    const { state, saveCreds } = await useMultiFileAuthState('auth_info_baileys');
    const { version, isLatest } = await fetchLatestBaileysVersion();
    
    console.log(`Using Baileys version v${version.join('.')}, isLatest: ${isLatest}`);
    const logger = pino({ level: 'silent' });
    
    const sock = makeWASocket({
        version,
        auth: state,
        printQRInTerminal: false,
        logger: logger,
        syncFullHistory: true, 
        browser: Browsers.macOS('Desktop')
    });

    sock.ev.on('creds.update', saveCreds);

    // --- SINGLE, UNIFIED CONNECTION LISTENER ---
    sock.ev.on('connection.update', async (update) => {
        const { connection, lastDisconnect, qr } = update;

        // 1. Generate Terminal QR Code
        if (qr) {
            const smallQR = await QRCode.toString(qr, {
                type: 'terminal',
                small: true,
                margin: 1,
                errorCorrectionLevel: 'L',
            });
            console.log(smallQR);
        }

        // 2. Handle Successful Connection
        if (connection === 'open') {
            resetBackoff();
            console.log('✅ Connected to WhatsApp! Waiting for history sync...');
        }

        // 3. Handle Disconnections & Reconnects
        if (connection === 'close') {
            const statusCode = lastDisconnect?.error?.output?.statusCode;
            
            if (statusCode === DisconnectReason.loggedOut) {
                console.log('🔒 Logged out – Please delete the auth_info_baileys folder and restart to re-scan the QR code.');
                return; // Stop the bot completely so it doesn't loop
            }

            const wait = nextBackoff();
            console.log(`⚠️ Connection closed (Status: ${statusCode}). Reconnecting in ${wait / 1000} seconds...`);
            
            setTimeout(() => {
                connectToWhatsApp();
            }, wait);
        }
    });

    // Handle History and Live Messages
    sock.ev.on('messaging-history.set', (data) => handleHistorySync(sock, data));
    sock.ev.on('messages.upsert', (m) => handleMessage(sock, m));
}

module.exports = { connectToWhatsApp };