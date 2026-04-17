FROM python:3.10.5-slim-buster
WORKDIR /opt/server

RUN apt update \
    && apt-get install --no-install-recommends -y libmagic-dev nginx procps gcc nodejs npm pkg-config \
    && npm config set registry https://registry.npmmirror.com \
    && npm install pm2 -g \
    && if [ ! -d /var/log/nginx ]; then mkdir /var/log/nginx; fi  

ADD requirements.txt .
RUN pip3 install  --no-cache-dir -r requirements.txt 

ADD . .

RUN cp -R config/nginx/* /etc/nginx/  \
    && chown -R www-data:www-data /etc/nginx \
    && unlink /etc/nginx/sites-enabled/default \
    && rm -rf configs/nginx

EXPOSE 8000

CMD ["/bin/bash", "config/entrypoints.sh"]