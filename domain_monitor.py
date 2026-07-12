import re
import requests
from bs4 import BeautifulSoup
import os
import cloudscraper

# ---------- 数据抓取配置 ----------
BASE_URL = "https://www.juyu.com/ykj/get_list"
COOKIE = os.getenv('COOKIE')

HEADERS = {
    'cookie': COOKIE,
    'origin': 'https://www.juyu.com',
    'referer': 'https://www.juyu.com/ykj/',
    'user-agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36',
    'x-requested-with': 'XMLHttpRequest',
}

DATA = {
    'dqsj_1': '3000',
    'psize': '50',
    'page': '1',
    'jgpx': '3',
}

PUSH_URL = "https://hpbnvpiosrzhgyxbrwpt.supabase.co/functions/v1/wx-push/domain-check"
SENDKEY = os.getenv('SENDKEY')
API_TOKEN = os.getenv('API_TOKEN')

def fetch_first_page():
    if not COOKIE:
        raise Exception("环境变量 COOKIE 未设置")
    resp = requests.post(BASE_URL, headers=HEADERS, data=DATA, timeout=30)
    resp.raise_for_status()
    result = resp.json()
    if result.get('code') != 1:
        raise Exception(f"请求失败: {result.get('msg')}")
    return result['html']

def parse_domains(html):
    soup = BeautifulSoup(html, 'html.parser')
    rows = soup.select('tbody tr')
    domains = []
    for row in rows:
        name_tag = row.find('a', class_='a_ym')
        if not name_tag:
            continue
        domain_name = name_tag.text.strip()
        tds = row.find_all('td')
        if len(tds) < 6:
            continue
        try:
            length = int(tds[1].text.strip())
        except:
            continue
        remain_text = tds[4].get_text()
        match = re.search(r'还剩(\d+)天', remain_text)
        if not match:
            continue
        remain_days = int(match.group(1))
        date_span = tds[4].find('span', class_='dqsj')
        expire_date = date_span.text.strip() if date_span else '未知'
        price_text = tds[5].get_text()
        price_match = re.search(r'(\d+)', price_text)
        if not price_match:
            continue
        price = int(price_match.group(1))
        domains.append({
            'name': domain_name,
            'length': length,
            'remain_days': remain_days,
            'expire_date': expire_date,
            'price': price
        })
    return domains

def filter_top5(domains):
    filtered = [d for d in domains if d['length'] < 20 and d['remain_days'] > 3000]
    sorted_list = sorted(filtered, key=lambda x: x['price'])
    return sorted_list[:5]

def send_notification(results):
    if not API_TOKEN:
        raise Exception("环境变量 API_TOKEN 未设置")
    if not SENDKEY:
        raise Exception("环境变量 SENDKEY 未设置")

    data_list = []
    for d in results:
        data_list.append({
            "domain": d['name'],
            "length": d['length'],
            "expire": d['expire_date'],
            "price": d['price']
        })

    payload = {
        "openid": SENDKEY,
        "title": "域名到期提醒",
        "data": data_list
    }

    push_headers = {
        "Authorization": API_TOKEN,
        "Content-Type": "application/json",
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
    }

    # 使用 cloudscraper 绕过 Cloudflare
    scraper = cloudscraper.create_scraper()
    resp = scraper.post(PUSH_URL, headers=push_headers, json=payload, timeout=30)
    print(f"推送状态码: {resp.status_code}")
    print(f"推送响应: {resp.text}")
    resp.raise_for_status()
    print("推送成功")

def main():
    print("正在获取第一页数据...")
    html = fetch_first_page()
    domains = parse_domains(html)
    print(f"解析到 {len(domains)} 条记录")
    top5 = filter_top5(domains)
    print(f"筛选后 Top5: {top5}")
    send_notification(top5)
    print("推送完成")

if __name__ == "__main__":
    main()
